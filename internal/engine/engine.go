// Package engine applies the scheduled Teams status and manages sleep
// prevention. All Teams UI automation runs on one worker goroutine.
package engine

import (
	"log"
	"path/filepath"
	"sync"
	"time"

	"teamsstatus/internal/config"
	"teamsstatus/internal/keepawake"
	"teamsstatus/internal/schedule"
	"teamsstatus/internal/teams"
)

const (
	retryMin   = time.Minute
	retryMax   = 15 * time.Minute
	resetKey   = "reset"
	jobTimeout = 90 * time.Second
)

// Automator is how the engine drives Teams (swappable in tests).
type Automator interface {
	Status(target string) (bool, string)
	Apply(target string, p *schedule.Presence) (string, error)
	Inspect(target string) (string, error)
}

// Slot summarises a resolved status for display.
type Slot struct {
	Presence string    `json:"presence"`
	Label    string    `json:"label"`
	Schedule string    `json:"schedule"`
	Until    time.Time `json:"until"`
}

type Status struct {
	Target         string    `json:"target"`
	Ready          bool      `json:"ready"`
	Info           string    `json:"info"`
	Paused         bool      `json:"paused"`
	Current        *Slot     `json:"current"`
	NextAt         time.Time `json:"nextAt"`
	Next           *Slot     `json:"next"`
	HasNext        bool      `json:"hasNext"`
	Applied        string    `json:"applied"` // label last set, "" = automatic
	LastUpdate     time.Time `json:"lastUpdate"`
	LastDetail     string    `json:"lastDetail"`
	LastError      string    `json:"lastError"`
	RetryAt        time.Time `json:"retryAt"`
	Busy           bool      `json:"busy"`
	SleepPrevented bool      `json:"sleepPrevented"`
}

type Engine struct {
	store    *config.Store
	awake    *keepawake.Controller
	auto     Automator
	OnChange func(Status) // called (from any goroutine) after status changes

	mu         sync.Mutex
	status     Status
	appliedKey string // presence applied, resetKey, or "" (unknown)
	weSet      bool   // we changed Teams away from automatic
	failures   int
	retryAfter time.Time

	jobs   chan func()
	kick   chan struct{}
	stop   chan struct{}
	closed sync.Once
}

func New(store *config.Store, auto Automator, awake *keepawake.Controller) *Engine {
	return &Engine{
		store: store, auto: auto, awake: awake,
		jobs: make(chan func(), 8), kick: make(chan struct{}, 1), stop: make(chan struct{}),
	}
}

// Start launches the ticker and worker goroutines.
func (e *Engine) Start() {
	go func() {
		for job := range e.jobs {
			job()
		}
	}()
	go func() {
		for {
			e.Tick()
			interval := time.Duration(e.store.Get().Settings.CheckIntervalSeconds) * time.Second
			select {
			case <-time.After(interval):
			case <-e.kick:
			case <-e.stop:
				return
			}
		}
	}()
}

// Kick re-evaluates immediately (after the user changes something).
func (e *Engine) Kick() {
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

func (e *Engine) Snapshot() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

func (e *Engine) changed() {
	if e.OnChange != nil {
		e.OnChange(e.Snapshot())
	}
}

func toSlot(r *schedule.Resolution) *Slot {
	if r == nil {
		return nil
	}
	return &Slot{Presence: string(r.Presence), Label: r.Presence.Label(), Schedule: r.Schedule.Name, Until: r.BlockEnd}
}

// Tick resolves the schedule, updates sleep prevention and, if needed,
// queues a Teams status change.
func (e *Engine) Tick() {
	cfg := e.store.Get()
	s := cfg.Settings
	now := time.Now()
	current := schedule.Resolve(cfg.Schedules, now)
	nextAt, next, hasNext := schedule.NextChange(cfg.Schedules, now)

	// Sleep prevention is independent of the Teams side: evaluated every tick,
	// unaffected by pause.
	wantAwake := s.KeepAwake == config.AwakeAlways || (s.KeepAwake == config.AwakeScheduled && current != nil)
	if err := e.awake.Set(wantAwake, s.KeepDisplayAwake); err != nil {
		log.Printf("keep awake: %v", err)
	}
	ready, info := e.auto.Status(s.Target)

	e.mu.Lock()
	e.status.Target, e.status.Ready, e.status.Info = s.Target, ready, info
	e.status.Paused = s.Paused
	e.status.Current, e.status.Next, e.status.NextAt, e.status.HasNext = toSlot(current), toSlot(next), nextAt, hasNext
	e.status.SleepPrevented = e.awake.Active()
	var job func()
	if ready && !e.status.Busy && !now.Before(e.retryAfter) {
		job = e.planLocked(s, current)
	}
	if job != nil {
		e.status.Busy = true
	}
	e.mu.Unlock()
	if job != nil {
		e.jobs <- job
	}
	e.changed()
}

// planLocked decides whether Teams needs changing; returns the job or nil.
func (e *Engine) planLocked(s config.Settings, current *schedule.Resolution) func() {
	target := s.Target
	if s.Paused || current == nil {
		if e.weSet && e.appliedKey != resetKey {
			return e.jobFor(target, nil, resetKey)
		}
		return nil
	}
	key := string(current.Presence)
	if key == e.appliedKey {
		return nil // UI automation: only act on changes
	}
	p := current.Presence
	return e.jobFor(target, &p, key)
}

func (e *Engine) jobFor(target string, p *schedule.Presence, key string) func() {
	return func() {
		detail, err := e.auto.Apply(target, p)
		e.finish(key, detail, err)
	}
}

func (e *Engine) finish(key, detail string, err error) {
	e.mu.Lock()
	now := time.Now()
	e.status.Busy = false
	e.status.LastUpdate = now
	if err == nil {
		e.appliedKey = key
		e.failures = 0
		e.retryAfter = time.Time{}
		e.status.RetryAt = time.Time{}
		e.status.LastError = ""
		e.status.LastDetail = detail
		if key == resetKey {
			e.weSet = false
			e.status.Applied = ""
			log.Printf("Teams status reset to automatic (%s)", detail)
		} else {
			e.weSet = true
			e.status.Applied = schedule.Presence(key).Label()
			log.Printf("Teams status set to %s (%s)", key, detail)
		}
	} else {
		e.appliedKey = ""
		e.failures++
		delay := retryMin << (e.failures - 1)
		if delay > retryMax || delay <= 0 {
			delay = retryMax
		}
		e.retryAfter = now.Add(delay)
		e.status.RetryAt = e.retryAfter
		e.status.LastError = err.Error()
		log.Printf("status change failed (retry in %s): %v", delay, err)
	}
	e.mu.Unlock()
	e.changed()
}

// ForceApply clears the retry back-off and re-applies the current status.
func (e *Engine) ForceApply() {
	e.mu.Lock()
	e.appliedKey = ""
	e.failures = 0
	e.retryAfter = time.Time{}
	e.status.RetryAt = time.Time{}
	e.mu.Unlock()
	e.Kick()
}

// SetPaused pauses/resumes Teams changes (sleep prevention is unaffected).
func (e *Engine) SetPaused(paused bool) error {
	err := e.store.Update(func(c *config.Config) error { c.Settings.Paused = paused; return nil })
	e.ForceApply()
	return err
}

// SetKeepAwake changes the sleep-prevention mode.
func (e *Engine) SetKeepAwake(mode string) error {
	err := e.store.Update(func(c *config.Config) error { c.Settings.KeepAwake = mode; return nil })
	e.Kick()
	return err
}

// Inspect runs the Teams UI diagnostic on the worker (blocks until done).
func (e *Engine) Inspect() string {
	target := e.store.Get().Settings.Target
	result := make(chan string, 1)
	e.jobs <- func() {
		text, err := e.auto.Inspect(target)
		if err != nil {
			text = "Inspect failed: " + err.Error()
		}
		result <- text
	}
	select {
	case text := <-result:
		return text
	case <-time.After(2 * jobTimeout):
		return "Inspect timed out."
	}
}

// Shutdown stops the loop, releases sleep prevention and (optionally)
// resets Teams to automatic if we changed it.
func (e *Engine) Shutdown() {
	e.closed.Do(func() {
		close(e.stop)
		_ = e.awake.Set(false, false)
		cfg := e.store.Get()
		e.mu.Lock()
		reset := cfg.Settings.ResetOnExit && e.weSet
		e.mu.Unlock()
		if reset {
			done := make(chan struct{})
			e.jobs <- func() {
				if _, err := e.auto.Apply(cfg.Settings.Target, nil); err != nil {
					log.Printf("reset on exit failed: %v", err)
				} else {
					log.Print("Teams status reset to automatic on exit")
				}
				close(done)
			}
			select {
			case <-done:
			case <-time.After(jobTimeout):
				log.Print("reset on exit timed out")
			}
		}
	})
}

// UIAutomator is the real Automator using the platform driver.
type UIAutomator struct{ LabelsPath string }

// DefaultAutomator returns a UIAutomator using ui_labels.json in the config dir.
func DefaultAutomator() *UIAutomator {
	return &UIAutomator{LabelsPath: filepath.Join(config.Dir(), "ui_labels.json")}
}

func (u *UIAutomator) Status(target string) (bool, string) { return teams.Status(target) }

func (u *UIAutomator) Apply(target string, p *schedule.Presence) (string, error) {
	d, err := teams.Open(target)
	if err != nil {
		return "", err
	}
	defer d.Close()
	return teams.Apply(d, p, teams.LoadLabels(u.LabelsPath))
}

func (u *UIAutomator) Inspect(target string) (string, error) {
	d, err := teams.Open(target)
	if err != nil {
		return "", err
	}
	defer d.Close()
	return teams.Inspect(d, teams.LoadLabels(u.LabelsPath)), nil
}
