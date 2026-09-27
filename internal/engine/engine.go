// Package engine applies the scheduled Teams status and manages sleep
// prevention. All Teams UI automation runs on one worker goroutine.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"teamsstatus/internal/config"
	"teamsstatus/internal/graph"
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

// Automator is how the engine changes Teams (swappable in tests).
type Automator interface {
	Status(target string) (bool, string)
	// Apply sets p (nil = back to automatic). until is when the scheduled
	// status ends (zero for a reset); Graph uses it as the expiry.
	Apply(target string, p *schedule.Presence, until time.Time) (string, error)
	Inspect(target string) (string, error)
	// RefreshEvery is how often to re-send an unchanged status (0 = never).
	RefreshEvery(target string) time.Duration
}

// expiryBuffer keeps a Graph status alive briefly past its block, so the next
// block's status replaces it without a gap.
const expiryBuffer = 2 * time.Minute

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
	appliedAt  time.Time
	weSet      bool // we changed Teams away from automatic
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
		job = e.planLocked(s, current, nextAt, hasNext, now)
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
func (e *Engine) planLocked(s config.Settings, current *schedule.Resolution, nextAt time.Time, hasNext bool, now time.Time) func() {
	target := s.Target
	if s.Paused || current == nil {
		if e.weSet && e.appliedKey != resetKey {
			return e.jobFor(target, nil, resetKey, time.Time{})
		}
		return nil
	}
	key := string(current.Presence)
	if key == e.appliedKey {
		// UI automation only acts on changes; Graph re-sends periodically
		// so long blocks don't outlive the status expiry.
		refresh := e.auto.RefreshEvery(target)
		if refresh == 0 || now.Sub(e.appliedAt) < refresh {
			return nil
		}
	}
	until := current.BlockEnd
	if hasNext && nextAt.Before(until) {
		until = nextAt
	}
	p := current.Presence
	return e.jobFor(target, &p, key, until.Add(expiryBuffer))
}

func (e *Engine) jobFor(target string, p *schedule.Presence, key string, until time.Time) func() {
	return func() {
		detail, err := e.auto.Apply(target, p, until)
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
		e.appliedAt = now
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
				if _, err := e.auto.Apply(cfg.Settings.Target, nil, time.Time{}); err != nil {
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

// Router is the real Automator: UI automation for the desktop/browser
// targets, Microsoft Graph for the graph target.
type Router struct {
	store      *config.Store
	labelsPath string
	cachePath  string

	mu        sync.Mutex
	graph     *graph.Client
	graphErr  error
	graphUser string
	signingIn bool
	signInErr string
}

// NewRouter uses ui_labels.json and the token cache in the config folder.
func NewRouter(store *config.Store) *Router {
	return &Router{
		store:      store,
		labelsPath: filepath.Join(config.Dir(), "ui_labels.json"),
		cachePath:  filepath.Join(config.Dir(), "graph-token-cache.bin"),
	}
}

// graphClient returns the Graph client for the current settings, recreating
// it when the client ID or tenant changes.
func (r *Router) graphClient() (*graph.Client, error) {
	s := r.store.Get().Settings
	r.mu.Lock()
	defer r.mu.Unlock()
	tenant := s.GraphTenant
	if tenant == "" {
		tenant = "organizations"
	}
	if r.graph != nil && r.graph.ClientID == s.GraphClientID && r.graph.Tenant == tenant {
		return r.graph, nil
	}
	if r.graph != nil || r.graphErr != nil {
		r.graphUser = ""
	}
	r.graph, r.graphErr = graph.New(s.GraphClientID, tenant, r.cachePath)
	if r.graph != nil {
		if user, ok := r.graph.Account(context.Background()); ok {
			r.graphUser = user
		}
	}
	return r.graph, r.graphErr
}

// GraphState is shown in the UI.
type GraphState struct {
	SignedInAs string `json:"signedInAs"`
	SigningIn  bool   `json:"signingIn"`
	Error      string `json:"error"`
}

func (r *Router) GraphState() GraphState {
	_, _ = r.graphClient()
	r.mu.Lock()
	defer r.mu.Unlock()
	st := GraphState{SignedInAs: r.graphUser, SigningIn: r.signingIn, Error: r.signInErr}
	if r.graphErr != nil && st.Error == "" {
		st.Error = r.graphErr.Error()
	}
	return st
}

// GraphSignIn starts the interactive browser sign-in in the background.
func (r *Router) GraphSignIn(done func()) error {
	c, err := r.graphClient()
	if err != nil {
		return err
	}
	r.mu.Lock()
	if r.signingIn {
		r.mu.Unlock()
		return nil
	}
	r.signingIn, r.signInErr = true, ""
	r.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		user, err := c.SignIn(ctx)
		r.mu.Lock()
		r.signingIn = false
		if err != nil {
			r.signInErr = err.Error()
			log.Printf("Graph sign-in failed: %v", err)
		} else {
			r.graphUser = user
			log.Printf("Graph: signed in as %s", user)
		}
		r.mu.Unlock()
		if done != nil {
			done()
		}
	}()
	return nil
}

// GraphSignOut forgets the account and its tokens.
func (r *Router) GraphSignOut() {
	if c, err := r.graphClient(); err == nil {
		c.SignOut(context.Background())
	}
	r.mu.Lock()
	r.graphUser, r.signInErr = "", ""
	r.mu.Unlock()
}

func (r *Router) Status(target string) (bool, string) {
	if target != config.TargetGraph {
		return teams.Status(target)
	}
	st := r.GraphState()
	switch {
	case st.SignedInAs != "":
		return true, "Microsoft Graph — " + st.SignedInAs
	case st.SigningIn:
		return false, "signing in to Microsoft (finish in your browser)…"
	case r.store.Get().Settings.GraphClientID == "":
		return false, "enter the app registration's client ID in Settings"
	}
	return false, "not signed in to Microsoft (click Sign in)"
}

func (r *Router) RefreshEvery(target string) time.Duration {
	if target == config.TargetGraph {
		return 30 * time.Minute
	}
	return 0
}

func (r *Router) Apply(target string, p *schedule.Presence, until time.Time) (string, error) {
	if target == config.TargetGraph {
		c, err := r.graphClient()
		if err != nil {
			return "", err
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if p == nil {
			return "Graph: cleared preferred presence", c.ClearPresence(ctx)
		}
		d := time.Until(until)
		if err := c.SetPresence(ctx, *p, d); err != nil {
			if errors.Is(err, graph.ErrNotSignedIn) {
				r.mu.Lock()
				r.graphUser = ""
				r.mu.Unlock()
			}
			return "", err
		}
		return fmt.Sprintf("Graph: set preferred presence (expires %s)", until.Format("Mon 15:04")), nil
	}
	d, err := teams.Open(target)
	if err != nil {
		return "", err
	}
	defer d.Close()
	return teams.Apply(d, p, teams.LoadLabels(r.labelsPath))
}

func (r *Router) Inspect(target string) (string, error) {
	if target == config.TargetGraph {
		return "Inspect only applies to the Teams desktop app and browser methods; the Graph method doesn't use the Teams UI.", nil
	}
	d, err := teams.Open(target)
	if err != nil {
		return "", err
	}
	defer d.Close()
	return teams.Inspect(d, teams.LoadLabels(r.labelsPath)), nil
}
