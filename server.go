package main

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"teamsstatus/internal/autostart"
	"teamsstatus/internal/config"
	"teamsstatus/internal/engine"
	"teamsstatus/internal/schedule"
	"teamsstatus/internal/teams"
)

//go:embed frontend
var frontendFS embed.FS

// server is the local HTTP API the UI (frontend/) talks to. It is only
// reachable by the app's own webview through Wails' asset server.
type server struct {
	store *config.Store
	eng   *engine.Engine
	graph graphControl
	mux   *http.ServeMux
}

// graphControl is the Microsoft Graph sign-in surface (engine.Router).
type graphControl interface {
	GraphState() engine.GraphState
	GraphSignIn(done func()) error
	GraphSignOut()
	Apply(target string, p *schedule.Presence, until time.Time) (string, error)
}

func newServer(store *config.Store, eng *engine.Engine, g graphControl) *server {
	s := &server{store: store, eng: eng, graph: g, mux: http.NewServeMux()}
	static, _ := fs.Sub(frontendFS, "frontend")
	s.mux.Handle("/", http.FileServer(http.FS(static)))
	s.mux.HandleFunc("GET /api/state", s.state)
	s.mux.HandleFunc("GET /api/preview", s.preview)
	s.mux.HandleFunc("POST /api/schedule", s.saveSchedule)
	s.mux.HandleFunc("POST /api/schedule/delete", s.deleteSchedule)
	s.mux.HandleFunc("POST /api/schedule/enable", s.enableSchedule)
	s.mux.HandleFunc("POST /api/settings", s.saveSettings)
	s.mux.HandleFunc("POST /api/action", s.action)
	s.mux.HandleFunc("POST /api/inspect", s.inspect)
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

type presenceInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

func (s *server) state(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()
	var presences []presenceInfo
	for _, p := range schedule.AllPresences {
		presences = append(presences, presenceInfo{string(p), p.Label()})
	}
	writeJSON(w, map[string]any{
		"status":        s.eng.Snapshot(),
		"settings":      cfg.Settings,
		"schedules":     cfg.Schedules,
		"launchAtLogin": autostart.Enabled(),
		"graph":         s.graph.GraphState(),
		"platform":      runtime.GOOS,
		"presences":     presences,
		"version":       version,
	})
}

// preview resolves the current week (Mon–Sun) in 15-minute slots.
func (s *server) preview(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()
	now := time.Now()
	offset := (int(now.Weekday()) + 6) % 7 // days since Monday
	monday := time.Date(now.Year(), now.Month(), now.Day()-offset, 0, 0, 0, 0, time.Local)
	type slot struct {
		P string `json:"p,omitempty"`
		S string `json:"s,omitempty"`
	}
	days := make([][]slot, 7)
	for d := 0; d < 7; d++ {
		days[d] = make([]slot, 96)
		for i := 0; i < 96; i++ {
			t := time.Date(monday.Year(), monday.Month(), monday.Day()+d, 0, i*15, 0, 0, time.Local)
			if res := schedule.Resolve(cfg.Schedules, t); res != nil {
				days[d][i] = slot{string(res.Presence), res.Schedule.Name}
			}
		}
	}
	writeJSON(w, map[string]any{"days": days, "today": offset, "nowMinutes": now.Hour()*60 + now.Minute()})
}

func (s *server) saveSchedule(w http.ResponseWriter, r *http.Request) {
	var sch schedule.Schedule
	if err := readJSON(r, &sch); err != nil {
		writeErr(w, err)
		return
	}
	sch.Name = strings.TrimSpace(sch.Name)
	if err := sch.Validate(); err != nil {
		writeErr(w, err)
		return
	}
	err := s.store.Update(func(c *config.Config) error {
		if sch.ID == "" {
			sch.ID = config.NewID()
			c.Schedules = append(c.Schedules, sch)
			return nil
		}
		for i := range c.Schedules {
			if c.Schedules[i].ID == sch.ID {
				c.Schedules[i] = sch
				return nil
			}
		}
		return errors.New("schedule not found")
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.eng.Kick()
	writeJSON(w, sch)
}

func (s *server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct{ ID string }
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Update(func(c *config.Config) error {
		for i := range c.Schedules {
			if c.Schedules[i].ID == req.ID {
				c.Schedules = append(c.Schedules[:i], c.Schedules[i+1:]...)
				break
			}
		}
		return nil
	})
	s.eng.Kick()
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *server) enableSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID      string
		Enabled bool
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Update(func(c *config.Config) error {
		for i := range c.Schedules {
			if c.Schedules[i].ID == req.ID {
				c.Schedules[i].Enabled = req.Enabled
			}
		}
		return nil
	})
	s.eng.Kick()
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		config.Settings
		LaunchAtLogin bool `json:"launchAtLogin"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	switch req.Target {
	case config.TargetDesktop, config.TargetBrowser, config.TargetGraph:
	default:
		writeErr(w, errors.New("unknown method"))
		return
	}
	req.GraphClientID = strings.TrimSpace(req.GraphClientID)
	req.GraphTenant = strings.TrimSpace(req.GraphTenant)
	if req.GraphTenant == "" {
		req.GraphTenant = "organizations"
	}
	if req.Target == config.TargetGraph && req.GraphClientID == "" {
		writeErr(w, errors.New("enter the app registration's Application (client) ID to use Microsoft Graph"))
		return
	}
	if req.CheckIntervalSeconds < 15 || req.CheckIntervalSeconds > 600 {
		writeErr(w, errors.New("check interval must be 15–600 seconds"))
		return
	}
	targetChanged := false
	err := s.store.Update(func(c *config.Config) error {
		req.Settings.Paused = c.Settings.Paused // pause is toggled separately
		targetChanged = c.Settings.Target != req.Target
		c.Settings = req.Settings
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if autostart.Supported() && req.LaunchAtLogin != autostart.Enabled() {
		if err := autostart.Set(req.LaunchAtLogin); err != nil {
			writeErr(w, err)
			return
		}
	}
	if targetChanged {
		s.eng.ForceApply()
	} else {
		s.eng.Kick()
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *server) action(w http.ResponseWriter, r *http.Request) {
	var req struct{ Action, Mode string }
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	var err error
	switch req.Action {
	case "pause":
		err = s.eng.SetPaused(true)
	case "resume":
		err = s.eng.SetPaused(false)
	case "apply":
		s.eng.ForceApply()
	case "grant":
		if !teams.HasPermission(true) {
			err = openAccessibilitySettings()
		}
		s.eng.ForceApply()
	case "graphSignIn":
		err = s.graph.GraphSignIn(s.eng.ForceApply)
	case "graphSignOut":
		// Hand Teams back to automatic first; that needs the token.
		if _, clearErr := s.graph.Apply(config.TargetGraph, nil, time.Time{}); clearErr != nil {
			log.Printf("clear presence before sign-out: %v", clearErr)
		}
		s.graph.GraphSignOut()
		s.eng.ForceApply()
	case "keepAwake":
		err = s.eng.SetKeepAwake(req.Mode)
	case "openLabels":
		path := filepath.Join(config.Dir(), "ui_labels.json")
		if _, statErr := os.Stat(path); statErr != nil {
			data, _ := json.MarshalIndent(teams.DefaultLabels(), "", "  ")
			err = os.WriteFile(path, data, 0o644)
		}
		if err == nil {
			err = openPath(path)
		}
	case "openFolder":
		err = openPath(config.Dir())
	default:
		err = errors.New("unknown action")
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *server) inspect(w http.ResponseWriter, r *http.Request) {
	text := s.eng.Inspect()
	path := filepath.Join(config.Dir(), "teams-ui-inspection.txt")
	_ = os.WriteFile(path, []byte(text), 0o644)
	writeJSON(w, map[string]string{"text": text, "path": path})
}
