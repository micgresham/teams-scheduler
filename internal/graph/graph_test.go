package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"teamsstatus/internal/schedule"
)

type recorded struct {
	path string
	body map[string]string
}

// graphWithToken returns a client whose token source is stubbed and whose
// Graph base URL points at a fake server.
func fakeGraph(t *testing.T, status int) (*Client, *[]recorded) {
	var calls []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing bearer token")
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, recorded{r.URL.Path, body})
		w.WriteHeader(status)
		if status >= 300 {
			w.Write([]byte(`{"error":{"code":"Forbidden","message":"Insufficient privileges"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	c, err := New("00000000-0000-0000-0000-000000000001", "", filepath.Join(t.TempDir(), "cache.bin"))
	if err != nil {
		t.Fatal(err)
	}
	c.base = srv.URL
	tokenOverride = func(context.Context) (string, error) { return "test-token", nil }
	t.Cleanup(func() { tokenOverride = nil })
	return c, &calls
}

func TestSetPresenceRequest(t *testing.T) {
	c, calls := fakeGraph(t, http.StatusOK)
	if err := c.SetPresence(context.Background(), schedule.Offline, 90*time.Minute); err != nil {
		t.Fatal(err)
	}
	got := (*calls)[0]
	if got.path != "/me/presence/setUserPreferredPresence" || got.body["availability"] != "Offline" ||
		got.body["activity"] != "OffWork" || got.body["expirationDuration"] != "PT1H30M0S" {
		t.Errorf("unexpected request %+v", got)
	}
}

func TestExpirationClamped(t *testing.T) {
	c, calls := fakeGraph(t, http.StatusOK)
	c.SetPresence(context.Background(), schedule.Busy, time.Minute)
	c.SetPresence(context.Background(), schedule.Busy, 72*time.Hour)
	if (*calls)[0].body["expirationDuration"] != "PT0H5M0S" || (*calls)[1].body["expirationDuration"] != "PT24H0M0S" {
		t.Errorf("clamping failed: %+v", *calls)
	}
}

func TestClearPresence(t *testing.T) {
	c, calls := fakeGraph(t, http.StatusOK)
	if err := c.ClearPresence(context.Background()); err != nil {
		t.Fatal(err)
	}
	if (*calls)[0].path != "/me/presence/clearUserPreferredPresence" {
		t.Errorf("path %s", (*calls)[0].path)
	}
}

func TestForbiddenExplained(t *testing.T) {
	c, _ := fakeGraph(t, http.StatusForbidden)
	err := c.SetPresence(context.Background(), schedule.Busy, time.Hour)
	if err == nil || !strings.Contains(err.Error(), "Presence.ReadWrite") {
		t.Errorf("got %v", err)
	}
}

func TestNotSignedIn(t *testing.T) {
	c, err := New("00000000-0000-0000-0000-000000000001", "organizations", filepath.Join(t.TempDir(), "cache.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Account(context.Background()); ok {
		t.Error("fresh client should have no account")
	}
	if err := c.ClearPresence(context.Background()); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Errorf("got %v", err)
	}
}

func TestRequiresClientID(t *testing.T) {
	if _, err := New(" ", "", "x"); err == nil {
		t.Error("expected error for empty client ID")
	}
}

func TestProtectRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("touches the OS keychain")
	}
	plain := []byte(`{"cache":"secret"}`)
	enc, err := protect(plain)
	if err != nil {
		t.Skipf("secure storage unavailable: %v", err)
	}
	if strings.Contains(string(enc), "secret") {
		t.Error("cache not encrypted")
	}
	dec, err := unprotect(enc)
	if err != nil || string(dec) != string(plain) {
		t.Errorf("round trip failed: %v %q", err, dec)
	}
}

// TestSignInStartsBrowserFlow checks the URL a real sign-in would open
// (authorization code + PKCE, loopback redirect, the right scopes). It needs
// network access to Microsoft's login endpoint and skips without it.
func TestSignInStartsBrowserFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("network")
	}
	c, err := New("11111111-2222-3333-4444-555555555555", "organizations", filepath.Join(t.TempDir(), "cache.bin"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var opened string
	c.openURL = func(u string) error { opened = u; cancel(); return nil }
	_, _ = c.SignIn(ctx)
	if opened == "" {
		t.Skip("sign-in URL not produced (offline?)")
	}
	for _, want := range []string{
		"https://login.microsoftonline.com/organizations/oauth2/v2.0/authorize",
		"client_id=11111111-2222-3333-4444-555555555555",
		"redirect_uri=http%3A%2F%2Flocalhost%3A",
		"code_challenge_method=S256",
		"Presence.ReadWrite",
		"offline_access",
	} {
		if !strings.Contains(opened, want) {
			t.Errorf("sign-in URL missing %q:\n%s", want, opened)
		}
	}
}
