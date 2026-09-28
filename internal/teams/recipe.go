// Package teams changes the user's status by operating the Teams UI the way
// a person would: profile picture → "<status>, change status" → status.
//
// The new Teams desktop app is the Teams web app in a native shell, so the
// same controls exist in the desktop app and in a browser. Platform drivers
// (macOS Accessibility, Windows UI Automation) expose them as Elements.
package teams

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"teamsstatus/internal/schedule"
)

// Element is a driver-neutral handle to a control.
type Element struct {
	Role     string // button, menuitem, listitem, radio, checkbox, link
	Name     string
	HasPopup bool // opens a menu/submenu (e.g. the status opener), rather than being a choice
	Native   any
}

// Driver exposes the controls of one Teams window.
type Driver interface {
	Elements() []Element
	Press(Element) error
	Dismiss() // close one level of menu (Escape)
	Close()   // restore window state (re-hide, re-minimise, ...)
}

// itemRoles are roles that entries of a menu/list get.
var itemRoles = map[string]bool{"menuitem": true, "option": true, "radio": true, "listitem": true}

// Labels are the regular expressions (case-insensitive) used to find controls.
// They can be overridden with ui_labels.json in the config folder, e.g. for a
// non-English Teams or after a Teams UI change.
type Labels struct {
	ProfileButton []string            `json:"profileButton"`
	StatusOpener  []string            `json:"statusOpener"`
	StatusItems   map[string][]string `json:"statusItems"`
	ResetItem     []string            `json:"resetItem"`
}

// DefaultLabels match English Teams (verified against new Teams, 2026).
func DefaultLabels() Labels {
	return Labels{
		// Title-bar avatar, e.g. "Your profile, status Available".
		ProfileButton: []string{`^your profile`, `^profile\b`, `account manager`, `profile picture`},
		// In the flyout, e.g. "Available, change status".
		StatusOpener: []string{`, change status$`, `^set status$`, `^(current )?status\b`},
		// Exact names: the opener ("Available, change status") must not match.
		StatusItems: map[string][]string{
			string(schedule.Available):    {`^available$`},
			string(schedule.Busy):         {`^busy$`},
			string(schedule.DoNotDisturb): {`^do not disturb$`},
			string(schedule.BeRightBack):  {`^be right back$`},
			string(schedule.Away):         {`^appear away$`, `^away$`},
			string(schedule.Offline):      {`^appear offline$`},
		},
		ResetItem: []string{`^reset status$`},
	}
}

// LoadLabels merges overrides from path (if present) over the defaults.
func LoadLabels(path string) Labels {
	labels := DefaultLabels()
	data, err := os.ReadFile(path)
	if err != nil {
		return labels
	}
	var o Labels
	if err := json.Unmarshal(data, &o); err != nil {
		log.Printf("ignoring invalid %s: %v", path, err)
		return labels
	}
	if len(o.ProfileButton) > 0 {
		labels.ProfileButton = o.ProfileButton
	}
	if len(o.StatusOpener) > 0 {
		labels.StatusOpener = o.StatusOpener
	}
	if len(o.ResetItem) > 0 {
		labels.ResetItem = o.ResetItem
	}
	for k, v := range o.StatusItems {
		labels.StatusItems[k] = v
	}
	return labels
}

type query struct {
	patterns []*regexp.Regexp
	exclude  map[string]bool
	roles    map[string]bool
}

func compile(patterns []string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, p := range patterns {
		if re, err := regexp.Compile("(?i)" + p); err == nil {
			out = append(out, re)
		} else {
			log.Printf("bad label pattern %q: %v", p, err)
		}
	}
	return out
}

// find polls until a control matching q appears (patterns in priority order).
func find(d Driver, q query, timeout time.Duration) (Element, bool) {
	deadline := time.Now().Add(timeout)
	for {
		var candidates []Element
		for _, e := range d.Elements() {
			if e.Name != "" && !q.exclude[e.Name] && (q.roles == nil || q.roles[e.Role]) {
				candidates = append(candidates, e)
			}
		}
		for _, re := range q.patterns {
			for _, e := range candidates {
				if re.MatchString(e.Name) {
					return e, true
				}
			}
		}
		if !time.Now().Before(deadline) {
			return Element{}, false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// Optional driver capabilities.
type (
	altPresser interface {
		// PressAlt tries fallback activation method n; ok=false when out of methods.
		PressAlt(e Element, n int) (method string, ok bool)
	}
	describer interface{ Describe() string }
)

// uiSignature summarises the visible controls, to detect whether a press did anything.
func uiSignature(d Driver) string {
	var b strings.Builder
	for _, e := range d.Elements() {
		b.WriteString(e.Role)
		b.WriteString(e.Name)
		b.WriteByte(0)
	}
	return b.String()
}

func waitForChange(d Driver, before string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if uiSignature(d) != before {
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// pressAndConfirm presses e and checks the UI changed (a menu opened). If it
// didn't, it tries the driver's fallback methods (re-finding the control with
// refind before each). Returns how the press succeeded.
func pressAndConfirm(d Driver, e Element, refind func() (Element, bool)) (string, error) {
	before := uiSignature(d)
	if err := d.Press(e); err != nil {
		return "", err
	}
	if waitForChange(d, before, 2*time.Second) {
		return "press", nil
	}
	ap, ok := d.(altPresser)
	if !ok {
		return "", fmt.Errorf("pressing %q had no visible effect", e.Name)
	}
	for n := 0; ; n++ {
		el, found := refind()
		if !found {
			return "fallback", nil // the control went away: something happened
		}
		method, more := ap.PressAlt(el, n)
		if !more {
			break
		}
		if waitForChange(d, before, 2*time.Second) {
			log.Printf("pressing %q needed fallback: %s", e.Name, method)
			return method, nil
		}
	}
	msg := fmt.Sprintf("pressing %q had no visible effect (tried press, focus + Return, mouse click)", e.Name)
	if ds, ok := d.(describer); ok {
		msg += "; Teams windows: " + strings.ReplaceAll(ds.Describe(), "\n", " | ")
	}
	return "", errors.New(msg)
}

// ErrNotFound is wrapped by errors about missing controls.
var ErrNotFound = errors.New("control not found")

// closeMenus presses Escape until the avatar is back: Teams menus are nested
// and an open flyout hides the rest of the window from accessibility tools.
func closeMenus(d Driver, labels Labels) {
	profile := query{patterns: compile(labels.ProfileButton)}
	for i := 0; i < 3; i++ {
		d.Dismiss()
		time.Sleep(400 * time.Millisecond)
		if _, ok := find(d, profile, 0); ok {
			return
		}
	}
	log.Print("a Teams menu may still be open")
}

// Apply sets the status (nil = "Reset status", i.e. back to automatic).
// It returns a short description of the clicks made.
func Apply(d Driver, presence *schedule.Presence, labels Labels) (string, error) {
	var steps []string
	var target []string
	what := "Reset status"
	if presence == nil {
		target = labels.ResetItem
	} else {
		target = labels.StatusItems[string(*presence)]
		what = presence.Label()
	}

	profileQ := query{patterns: compile(labels.ProfileButton)}
	profile, ok := find(d, profileQ, 2*time.Second)
	if !ok {
		closeMenus(d, labels) // a menu left open hides the rest of Teams
		profile, ok = find(d, profileQ, 6*time.Second)
	}
	if !ok {
		return "", fmt.Errorf("%w: couldn't find the Teams profile picture button (try Inspect Teams UI)", ErrNotFound)
	}
	how, err := pressAndConfirm(d, profile, func() (Element, bool) { return find(d, profileQ, time.Second) })
	if err != nil {
		return "", err
	}
	steps = append(steps, fmt.Sprintf("pressed %q (%s)", profile.Name, how))
	defer func() {
		time.Sleep(400 * time.Millisecond)
		closeMenus(d, labels)
	}()

	excl := map[string]bool{profile.Name: true}
	targetQ := query{patterns: compile(target), exclude: excl, roles: itemRoles}
	// Some layouts show the status list straight away.
	item, ok := find(d, targetQ, 1500*time.Millisecond)
	if !ok {
		var opener Element
		opener, ok = find(d, query{patterns: compile(labels.StatusOpener), exclude: excl}, 5*time.Second)
		if !ok {
			return "", fmt.Errorf("%w: opened the profile menu but couldn't find the status button", ErrNotFound)
		}
		openerQ := query{patterns: compile(labels.StatusOpener), exclude: excl}
		how, err := pressAndConfirm(d, opener, func() (Element, bool) { return find(d, openerQ, time.Second) })
		if err != nil {
			return "", err
		}
		steps = append(steps, fmt.Sprintf("pressed %q (%s)", opener.Name, how))
		if item, ok = find(d, targetQ, 4*time.Second); !ok {
			excl2 := map[string]bool{profile.Name: true, opener.Name: true}
			item, ok = find(d, query{patterns: compile(target), exclude: excl2}, time.Second)
		}
	}
	if !ok {
		return "", fmt.Errorf("%w: opened the status list but couldn't find %q", ErrNotFound, what)
	}
	if err := d.Press(item); err != nil {
		return "", err
	}
	steps = append(steps, fmt.Sprintf("pressed %q", item.Name))
	return strings.Join(steps, " → "), nil
}

// Inspect lists visible controls before/after opening the profile and status
// menus, for diagnosing label mismatches. It doesn't change the status.
func Inspect(d Driver, labels Labels) string {
	var b strings.Builder
	list := func(title string) {
		fmt.Fprintf(&b, "== %s ==\n", title)
		for _, e := range d.Elements() {
			if e.Name != "" {
				popup := ""
				if e.HasPopup {
					popup = " ▸"
				}
				fmt.Fprintf(&b, "  [%s%s] %s\n", e.Role, popup, e.Name)
			}
		}
		b.WriteString("\n")
	}
	profileQ := query{patterns: compile(labels.ProfileButton)}
	if _, ok := find(d, profileQ, 2*time.Second); !ok {
		closeMenus(d, labels) // a menu left open hides the rest of Teams
	}
	windows := func(title string) {
		if ds, ok := d.(describer); ok {
			fmt.Fprintf(&b, "== %s ==\n  %s\n\n", title, strings.ReplaceAll(ds.Describe(), "\n", "\n  "))
		}
	}
	windows("Teams windows")
	list("Controls visible now")
	profile, ok := find(d, profileQ, 3*time.Second)
	if !ok {
		b.WriteString("(no control matched profileButton)\n")
		return b.String()
	}
	how, err := pressAndConfirm(d, profile, func() (Element, bool) { return find(d, profileQ, time.Second) })
	if err != nil {
		fmt.Fprintf(&b, "(%v)\n\n", err)
	} else {
		time.Sleep(700 * time.Millisecond)
	}
	windows("Teams windows after pressing profile button")
	list(fmt.Sprintf("After pressing profile button %q (%s)", profile.Name, how))
	openerQ := query{patterns: compile(labels.StatusOpener), exclude: map[string]bool{profile.Name: true}}
	opener, ok := find(d, openerQ, 2*time.Second)
	if ok {
		how, err := pressAndConfirm(d, opener, func() (Element, bool) { return find(d, openerQ, time.Second) })
		if err != nil {
			fmt.Fprintf(&b, "(%v)\n\n", err)
		} else {
			time.Sleep(700 * time.Millisecond)
		}
		list(fmt.Sprintf("After pressing status button %q (%s)", opener.Name, how))
	} else {
		b.WriteString("(no control matched statusOpener)\n")
	}
	closeMenus(d, labels)
	return b.String()
}
