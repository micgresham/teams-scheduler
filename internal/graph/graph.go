// Package graph sets Teams presence through Microsoft Graph, using an
// Entra ID (Azure AD) app registration supplied by the user.
//
// Sign-in is the standard interactive flow for desktop apps (authorization
// code + PKCE in the system browser, redirect to http://localhost), done by
// MSAL. The app acts only as the signed-in user (delegated permissions).
package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"

	"teamsstatus/internal/schedule"
)

// Scopes requested at sign-in (offline_access is added by MSAL).
var Scopes = []string{"Presence.ReadWrite", "User.Read"}

const (
	graphBase     = "https://graph.microsoft.com/v1.0"
	minExpiration = 5 * time.Minute
	maxExpiration = 24 * time.Hour
)

// ErrNotSignedIn means an interactive sign-in is needed.
var ErrNotSignedIn = errors.New("not signed in to Microsoft (click Sign in)")

type Client struct {
	ClientID string
	Tenant   string
	app      public.Client
	cache    fileCache
	base     string
	http     *http.Client
	openURL  func(string) error // tests: capture the sign-in URL instead of opening a browser
}

// New creates a client. tenant is a tenant ID, domain, or "organizations".
func New(clientID, tenant, cachePath string) (*Client, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, errors.New("enter the app registration's Application (client) ID in Settings")
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		tenant = "organizations"
	}
	fc := fileCache{path: cachePath}
	app, err := public.New(clientID,
		public.WithAuthority("https://login.microsoftonline.com/"+tenant),
		public.WithCache(fc))
	if err != nil {
		return nil, fmt.Errorf("invalid client ID or tenant: %w", err)
	}
	return &Client{ClientID: clientID, Tenant: tenant, app: app, cache: fc, base: graphBase,
		http: &http.Client{Timeout: 30 * time.Second}}, nil
}

// Account returns the signed-in user's name, if any.
func (c *Client) Account(ctx context.Context) (string, bool) {
	accounts, err := c.app.Accounts(ctx)
	if err != nil || len(accounts) == 0 {
		return "", false
	}
	return accounts[0].PreferredUsername, true
}

// SignIn opens the system browser for Microsoft sign-in and waits for it.
func (c *Client) SignIn(ctx context.Context) (string, error) {
	opts := []public.AcquireInteractiveOption{public.WithRedirectURI("http://localhost")}
	if c.openURL != nil {
		opts = append(opts, public.WithOpenURL(c.openURL))
	}
	res, err := c.app.AcquireTokenInteractive(ctx, Scopes, opts...)
	if err != nil {
		return "", explainAuthError(err)
	}
	return res.Account.PreferredUsername, nil
}

// SignOut forgets the account and deletes the token cache.
func (c *Client) SignOut(ctx context.Context) {
	if accounts, err := c.app.Accounts(ctx); err == nil {
		for _, a := range accounts {
			_ = c.app.RemoveAccount(ctx, a)
		}
	}
	c.cache.clear()
}

// tokenOverride lets tests bypass MSAL.
var tokenOverride func(context.Context) (string, error)

func (c *Client) token(ctx context.Context) (string, error) {
	if tokenOverride != nil {
		return tokenOverride(ctx)
	}
	accounts, err := c.app.Accounts(ctx)
	if err != nil || len(accounts) == 0 {
		return "", ErrNotSignedIn
	}
	res, err := c.app.AcquireTokenSilent(ctx, Scopes, public.WithSilentAccount(accounts[0]))
	if err != nil {
		return "", fmt.Errorf("%w (%v)", ErrNotSignedIn, err)
	}
	return res.AccessToken, nil
}

// graphActivity maps a presence to the activity Graph requires with it.
func graphActivity(p schedule.Presence) string {
	if p == schedule.Offline {
		return "OffWork"
	}
	return string(p)
}

func isoDuration(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	return fmt.Sprintf("PT%dH%dM%dS", s/3600, s%3600/60, s%60)
}

// SetPresence sets the user's preferred presence, expiring after d (clamped
// to 5 minutes – 24 hours) so a stale status lapses if the app stops.
func (c *Client) SetPresence(ctx context.Context, p schedule.Presence, d time.Duration) error {
	d = min(max(d, minExpiration), maxExpiration)
	return c.post(ctx, "/me/presence/setUserPreferredPresence", map[string]string{
		"availability":       string(p),
		"activity":           graphActivity(p),
		"expirationDuration": isoDuration(d),
	})
}

// ClearPresence hands presence back to Teams (automatic).
func (c *Client) ClearPresence(ctx context.Context) error {
	return c.post(ctx, "/me/presence/clearUserPreferredPresence", map[string]string{})
}

func (c *Client) post(ctx context.Context, path string, body any) error {
	tok, err := c.token(ctx)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Microsoft Graph unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var ge struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	_ = json.Unmarshal(raw, &ge)
	msg := fmt.Sprintf("Graph %d %s: %s", resp.StatusCode, ge.Error.Code, ge.Error.Message)
	if resp.StatusCode == http.StatusForbidden {
		msg += " (check the app registration has the delegated Presence.ReadWrite permission, and that consent was granted)"
	}
	return errors.New(msg)
}

// explainAuthError adds hints for the common Entra ID sign-in failures.
func explainAuthError(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "AADSTS65001") || strings.Contains(s, "AADSTS90094") || strings.Contains(s, "consent"):
		return fmt.Errorf("an administrator must grant consent for this app's permissions (Entra ID → App registrations → API permissions → Grant admin consent): %w", err)
	case strings.Contains(s, "AADSTS7000218"):
		return fmt.Errorf("turn on 'Allow public client flows' in the app registration's Authentication page: %w", err)
	case strings.Contains(s, "AADSTS50011"):
		return fmt.Errorf("add the redirect URI http://localhost under 'Mobile and desktop applications' in the app registration: %w", err)
	case strings.Contains(s, "AADSTS700016"):
		return fmt.Errorf("the client ID wasn't found in this tenant; check the Application (client) ID and tenant in Settings: %w", err)
	}
	return err
}
