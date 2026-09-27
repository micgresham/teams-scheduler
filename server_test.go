package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"teamsstatus/internal/config"
	"teamsstatus/internal/engine"
	"teamsstatus/internal/keepawake"
)

func newTestServer(t *testing.T) (*server, *config.Store) {
	store, err := config.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	return newServer(store, engine.New(store, demoAuto{}, keepawake.New()), demoAuto{}), store
}

func do(t *testing.T, s *server, method, path, body string) (int, map[string]any) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestServesUI(t *testing.T) {
	s, _ := newTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("Teams Status Scheduler")) {
		t.Fatalf("index: %d", rec.Code)
	}
}

func TestScheduleCRUD(t *testing.T) {
	s, store := newTestServer(t)
	code, out := do(t, s, "POST", "/api/schedule",
		`{"name":"Focus","priority":1,"enabled":true,"blocks":[{"days":[2,4],"start":"09:00","end":"11:00","presence":"DoNotDisturb"}]}`)
	if code != 200 || out["id"] == "" {
		t.Fatalf("create: %d %v", code, out)
	}
	id := out["id"].(string)
	if n := len(store.Get().Schedules); n != 2 {
		t.Fatalf("want 2 schedules, got %d", n)
	}
	code, _ = do(t, s, "POST", "/api/schedule/enable", `{"id":"`+id+`","enabled":false}`)
	if code != 200 || store.Get().Schedules[1].Enabled {
		t.Fatal("disable failed")
	}
	code, _ = do(t, s, "POST", "/api/schedule/delete", `{"id":"`+id+`"}`)
	if code != 200 || len(store.Get().Schedules) != 1 {
		t.Fatal("delete failed")
	}
}

func TestInvalidScheduleRejected(t *testing.T) {
	s, store := newTestServer(t)
	code, out := do(t, s, "POST", "/api/schedule", `{"name":"","priority":0,"blocks":[]}`)
	if code != 400 || !strings.Contains(out["error"].(string), "name") {
		t.Fatalf("got %d %v", code, out)
	}
	if len(store.Get().Schedules) != 1 {
		t.Fatal("invalid schedule was saved")
	}
}

func TestSettingsValidationAndPausePreserved(t *testing.T) {
	s, store := newTestServer(t)
	store.Update(func(c *config.Config) error { c.Settings.Paused = true; return nil })
	code, _ := do(t, s, "POST", "/api/settings", `{"target":"browser","checkIntervalSeconds":5,"keepAwake":"off"}`)
	if code != 400 {
		t.Fatal("interval 5s should be rejected")
	}
	code, _ = do(t, s, "POST", "/api/settings", `{"target":"browser","checkIntervalSeconds":30,"keepAwake":"always","launchAtLogin":false}`)
	st := store.Get().Settings
	if code != 200 || st.Target != "browser" || st.KeepAwake != "always" || !st.Paused {
		t.Fatalf("settings: %d %+v", code, st)
	}
}

func TestGraphSettingsNeedClientID(t *testing.T) {
	s, store := newTestServer(t)
	code, out := do(t, s, "POST", "/api/settings", `{"target":"graph","checkIntervalSeconds":60,"keepAwake":"off"}`)
	if code != 400 || !strings.Contains(out["error"].(string), "client) ID") {
		t.Fatalf("got %d %v", code, out)
	}
	code, _ = do(t, s, "POST", "/api/settings", `{"target":"graph","graphClientId":" abc ","graphTenant":"","checkIntervalSeconds":60,"keepAwake":"off"}`)
	st := store.Get().Settings
	if code != 200 || st.GraphClientID != "abc" || st.GraphTenant != "organizations" {
		t.Fatalf("got %d %+v", code, st)
	}
	if code, _ := do(t, s, "POST", "/api/settings", `{"target":"nope","checkIntervalSeconds":60}`); code != 400 {
		t.Fatal("unknown method accepted")
	}
}
