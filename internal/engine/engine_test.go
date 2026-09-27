package engine

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"teamsstatus/internal/config"
	"teamsstatus/internal/keepawake"
	"teamsstatus/internal/schedule"
)

type fakeAuto struct {
	mu    sync.Mutex
	calls []string
	fail  error
}

func (f *fakeAuto) Status(string) (bool, string)   { return true, "fake" }
func (f *fakeAuto) Inspect(string) (string, error) { return "", nil }
func (f *fakeAuto) Apply(_ string, p *schedule.Presence) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p == nil {
		f.calls = append(f.calls, "reset")
	} else {
		f.calls = append(f.calls, string(*p))
	}
	return "ok", f.fail
}
func (f *fakeAuto) get() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

var allDays = []time.Weekday{0, 1, 2, 3, 4, 5, 6}

func setup(t *testing.T, scheds []schedule.Schedule, awake string) (*Engine, *fakeAuto) {
	store, err := config.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.Update(func(c *config.Config) error {
		c.Schedules = scheds
		c.Settings.KeepAwake = awake
		return nil
	})
	f := &fakeAuto{}
	e := New(store, f, keepawake.New())
	go func() {
		for job := range e.jobs {
			job()
		}
	}()
	t.Cleanup(func() { e.awake.Set(false, false) })
	return e, f
}

func allDay(p schedule.Presence) schedule.Schedule {
	return schedule.Schedule{ID: "a", Name: "All day", Priority: 1, Enabled: true,
		Blocks: []schedule.Block{{Days: allDays, Start: "00:00", End: "00:00", Presence: p}}}
}

// settle runs a tick and waits for the queued job (if any) to finish.
func settle(e *Engine) {
	e.Tick()
	for i := 0; i < 200 && e.Snapshot().Busy; i++ {
		time.Sleep(5 * time.Millisecond)
	}
}

func TestScheduleAppliedInEveryKeepAwakeMode(t *testing.T) {
	for _, mode := range []string{config.AwakeOff, config.AwakeScheduled, config.AwakeAlways} {
		e, f := setup(t, []schedule.Schedule{allDay(schedule.Busy)}, mode)
		settle(e)
		if got := f.get(); len(got) != 1 || got[0] != "Busy" {
			t.Errorf("%s: calls %v", mode, got)
		}
		if e.Snapshot().SleepPrevented != (mode != config.AwakeOff) {
			t.Errorf("%s: sleep prevented = %v", mode, e.Snapshot().SleepPrevented)
		}
	}
}

func TestOnlyActsOnChange(t *testing.T) {
	e, f := setup(t, []schedule.Schedule{allDay(schedule.Busy)}, config.AwakeOff)
	settle(e)
	settle(e)
	settle(e)
	if got := f.get(); len(got) != 1 {
		t.Errorf("expected one click sequence, got %v", got)
	}
}

func TestPauseResetsButKeepsAwake(t *testing.T) {
	e, f := setup(t, []schedule.Schedule{allDay(schedule.Available)}, config.AwakeAlways)
	settle(e)
	e.store.Update(func(c *config.Config) error { c.Settings.Paused = true; return nil })
	settle(e)
	if got := f.get(); len(got) != 2 || got[1] != "reset" {
		t.Errorf("calls %v", got)
	}
	if !e.Snapshot().SleepPrevented {
		t.Error("pause must not release sleep prevention")
	}
}

func TestNoResetIfWeNeverSetAnything(t *testing.T) {
	e, f := setup(t, nil, config.AwakeOff)
	settle(e)
	if got := f.get(); len(got) != 0 {
		t.Errorf("calls %v", got)
	}
}

func TestFailureBacksOff(t *testing.T) {
	e, f := setup(t, []schedule.Schedule{allDay(schedule.Busy)}, config.AwakeOff)
	f.fail = errors.New("Teams not found")
	settle(e)
	settle(e)
	if got := f.get(); len(got) != 1 {
		t.Errorf("second tick should be inside the retry window: %v", got)
	}
	if e.Snapshot().LastError == "" || e.Snapshot().RetryAt.IsZero() {
		t.Error("error/retry not reported")
	}
	f.fail = nil
	e.ForceApply()
	settle(e)
	if got := f.get(); len(got) != 2 || e.Snapshot().Applied != "Busy" {
		t.Errorf("force apply: calls %v applied %q", got, e.Snapshot().Applied)
	}
}
