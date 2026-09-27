package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"teamsstatus/internal/config"
	"teamsstatus/internal/engine"
	"teamsstatus/internal/keepawake"
	"teamsstatus/internal/schedule"
)

type demoAuto struct{}

func (demoAuto) Status(string) (bool, string) { return true, "Teams desktop app" }
func (demoAuto) Apply(string, *schedule.Presence) (string, error) {
	return `pressed "Your profile" → pressed "Available, change status" → pressed "Busy"`, nil
}
func (demoAuto) Inspect(string) (string, error) { return "(demo)", nil }

// TestServeUI serves the real UI + API with demo data for manual/visual
// checks in a browser. Opt-in: TSS_SERVE_UI=127.0.0.1:8765 go test -run TestServeUI
func TestServeUI(t *testing.T) {
	addr := os.Getenv("TSS_SERVE_UI")
	if addr == "" {
		t.Skip("set TSS_SERVE_UI=host:port to run")
	}
	store, err := config.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	wk := []time.Weekday{1, 2, 3, 4, 5}
	store.Update(func(c *config.Config) error {
		c.Schedules[0].Enabled = true
		c.Schedules = append(c.Schedules,
			schedule.Schedule{ID: "focus", Name: "Focus mornings", Priority: 1, Enabled: true,
				Blocks: []schedule.Block{{Days: []time.Weekday{2, 4}, Start: "09:00", End: "11:00", Presence: schedule.DoNotDisturb}}},
			schedule.Schedule{ID: "standup", Name: "Stand-up", Priority: 5, Enabled: true,
				Blocks: []schedule.Block{{Days: wk, Start: "09:30", End: "09:45", Presence: schedule.Busy}}},
		)
		c.Settings.KeepAwake = config.AwakeScheduled
		return nil
	})
	eng := engine.New(store, demoAuto{}, keepawake.New())
	eng.Start()
	defer eng.Shutdown()
	srv := &http.Server{Addr: addr, Handler: newServer(store, eng)}
	go srv.ListenAndServe()
	time.Sleep(90 * time.Second)
	srv.Close()
}
