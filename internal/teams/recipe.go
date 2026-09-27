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
	Role   string // button, menuitem, listitem, radio, checkbox, link
	Name   string
	Native any
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
	if err := d.Press(profile); err != nil {
		return "", err
	}
	steps = append(steps, fmt.Sprintf("pressed %q", profile.Name))
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
		if err := d.Press(opener); err != nil {
			return "", err
		}
		steps = append(steps, fmt.Sprintf("pressed %q", opener.Name))
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
				fmt.Fprintf(&b, "  [%s] %s\n", e.Role, e.Name)
			}
		}
		b.WriteString("\n")
	}
	list("Controls visible now")
	profile, ok := find(d, query{patterns: compile(labels.ProfileButton)}, 3*time.Second)
	if !ok {
		b.WriteString("(no control matched profileButton)\n")
		return b.String()
	}
	_ = d.Press(profile)
	time.Sleep(1500 * time.Millisecond)
	list(fmt.Sprintf("After pressing profile button %q", profile.Name))
	opener, ok := find(d, query{patterns: compile(labels.StatusOpener), exclude: map[string]bool{profile.Name: true}}, 2*time.Second)
	if ok {
		_ = d.Press(opener)
		time.Sleep(1500 * time.Millisecond)
		list(fmt.Sprintf("After pressing status button %q", opener.Name))
	} else {
		b.WriteString("(no control matched statusOpener)\n")
	}
	closeMenus(d, labels)
	return b.String()
}
