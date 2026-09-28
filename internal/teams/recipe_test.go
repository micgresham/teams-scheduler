package teams

import (
	"testing"

	"teamsstatus/internal/schedule"
)

// fakeTeams mimics what new Teams exposes via accessibility (observed 2026):
// the avatar button opens a flyout whose first item is "<status>, change
// status"; that opens the status list. While the flyout is open the rest of the
// window is hidden (modal). Escape closes one menu level.
type fakeTeams struct {
	status     string // "" = automatic
	flyoutOpen bool
	listOpen   bool
	presses    []string
}

func (f *fakeTeams) shown() string {
	if f.status == "" {
		return "Available"
	}
	return f.status
}

func (f *fakeTeams) Elements() []Element {
	if !f.flyoutOpen {
		return []Element{
			{Role: "button", Name: "Back"},
			{Role: "button", Name: "Settings and more"},
			{Role: "button", Name: "Your profile, status " + f.shown()},
			{Role: "checkbox", Name: "Chat"},
		}
	}
	els := []Element{
		{Role: "menuitem", Name: f.shown() + ", change status"},
		{Role: "menuitem", Name: "Set status message"},
		{Role: "button", Name: "View your profile"},
	}
	if f.listOpen {
		for _, s := range []string{"Available", "Busy", "Do not disturb", "Be right back", "Appear away", "Appear offline", "Duration", "Reset status"} {
			els = append([]Element{{Role: "menuitem", Name: s}}, els...)
		}
	}
	return els
}

func (f *fakeTeams) Press(e Element) error {
	f.presses = append(f.presses, e.Name)
	switch {
	case e.Role == "button" && len(e.Name) > 12 && e.Name[:12] == "Your profile":
		f.flyoutOpen = !f.flyoutOpen
	case e.Name == f.shown()+", change status":
		f.listOpen = true
	case f.listOpen && e.Name == "Reset status":
		f.status, f.listOpen = "", false
	case f.listOpen && e.Name != "Duration":
		f.status, f.listOpen = e.Name, false
	}
	return nil
}

func (f *fakeTeams) Dismiss() {
	if f.listOpen {
		f.listOpen = false
	} else {
		f.flyoutOpen = false
	}
}

func (f *fakeTeams) Close() {}

func p(x schedule.Presence) *schedule.Presence { return &x }

func TestApplyEachStatus(t *testing.T) {
	for _, pr := range schedule.AllPresences {
		f := &fakeTeams{status: "Busy"}
		if pr == schedule.Busy {
			f.status = "Available"
		}
		if _, err := Apply(f, p(pr), DefaultLabels()); err != nil {
			t.Fatalf("%s: %v", pr, err)
		}
		if f.status != pr.Label() {
			t.Errorf("%s: status is %q", pr, f.status)
		}
		if f.flyoutOpen || f.listOpen {
			t.Errorf("%s: menus left open", pr)
		}
	}
}

func TestAvailableWhileShowingAvailable(t *testing.T) {
	// Automatic status displays as "Available"; forcing Available must still
	// pick the list item, not the "Available, change status" opener.
	f := &fakeTeams{}
	if _, err := Apply(f, p(schedule.Available), DefaultLabels()); err != nil {
		t.Fatal(err)
	}
	if f.status != "Available" {
		t.Errorf("status %q, presses %v", f.status, f.presses)
	}
}

func TestReset(t *testing.T) {
	f := &fakeTeams{status: "Do not disturb"}
	if _, err := Apply(f, nil, DefaultLabels()); err != nil {
		t.Fatal(err)
	}
	if f.status != "" {
		t.Errorf("status %q", f.status)
	}
}

func TestRecoversFromMenuLeftOpen(t *testing.T) {
	f := &fakeTeams{status: "Busy", flyoutOpen: true, listOpen: true}
	if _, err := Apply(f, p(schedule.BeRightBack), DefaultLabels()); err != nil {
		t.Fatal(err)
	}
	if f.status != "Be right back" || f.flyoutOpen {
		t.Errorf("status %q flyout %v", f.status, f.flyoutOpen)
	}
}

func TestInspectDoesNotChangeStatus(t *testing.T) {
	f := &fakeTeams{status: "Busy"}
	out := Inspect(f, DefaultLabels())
	if f.status != "Busy" || f.flyoutOpen {
		t.Errorf("inspect changed state: %q %v", f.status, f.flyoutOpen)
	}
	for _, want := range []string{"[menuitem] Busy, change status", "[menuitem] Do not disturb"} {
		if !contains(out, want) {
			t.Errorf("inspect output missing %q:\n%s", want, out)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// stubbornTeams ignores plain presses on the avatar (as seen in some work
// tenants); only the second fallback (mouse click) opens the menu.
type stubbornTeams struct {
	fakeTeams
	alts []string
}

func (s *stubbornTeams) Press(e Element) error {
	if len(e.Name) > 12 && e.Name[:12] == "Your profile" {
		s.presses = append(s.presses, "ignored:"+e.Name)
		return nil
	}
	return s.fakeTeams.Press(e)
}

func (s *stubbornTeams) PressAlt(e Element, n int) (string, bool) {
	switch n {
	case 0:
		s.alts = append(s.alts, "focus")
		return "focus + Return key", true // no effect
	case 1:
		s.alts = append(s.alts, "click")
		_ = s.fakeTeams.Press(e)
		return "mouse click", true
	}
	return "", false
}

func TestFallbackPressOpensMenu(t *testing.T) {
	f := &stubbornTeams{fakeTeams: fakeTeams{status: "Busy"}}
	detail, err := Apply(f, p(schedule.DoNotDisturb), DefaultLabels())
	if err != nil {
		t.Fatal(err)
	}
	if f.status != "Do not disturb" || len(f.alts) != 2 || !contains(detail, "mouse click") {
		t.Errorf("status %q alts %v detail %q", f.status, f.alts, detail)
	}
}

// deadTeams never reacts: the error must say so rather than "not found".
type deadTeams struct{ fakeTeams }

func (d *deadTeams) Press(Element) error                  { return nil }
func (d *deadTeams) PressAlt(Element, int) (string, bool) { return "", false }

func TestNoEffectReported(t *testing.T) {
	_, err := Apply(&deadTeams{}, p(schedule.Busy), DefaultLabels())
	if err == nil || !contains(err.Error(), "no visible effect") {
		t.Errorf("got %v", err)
	}
}
