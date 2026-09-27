//go:build darwin && realteams

// Live test against the installed new Teams desktop app. Changes your status
// (and resets it to automatic at the end). Run with:
//
//	go test -tags realteams -run TestRealTeams -v ./internal/teams/
package teams

import (
	"regexp"
	"testing"
	"time"

	"teamsstatus/internal/config"
	"teamsstatus/internal/schedule"
)

func avatar(t *testing.T) string {
	d, err := Open(config.TargetDesktop)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	re := regexp.MustCompile(`(?i)^your profile`)
	for _, e := range d.Elements() {
		if re.MatchString(e.Name) {
			return e.Name
		}
	}
	return "(avatar not visible — menu open?)"
}

func TestRealTeams(t *testing.T) {
	defer showTeams()
	steps := []struct {
		setup func()
		label string
		p     *schedule.Presence
		want  string
	}{
		{nil, "normal → Busy", p(schedule.Busy), "status Busy"},
		{minimizeTeams, "minimised → Do not disturb", p(schedule.DoNotDisturb), "status Do not disturb"},
		{nil, "minimised → Available", p(schedule.Available), "status Available"},
		{hideTeams, "hidden → Appear away", p(schedule.Away), "status Away"},
		{minimizeTeams, "hidden+minimised → Be right back", p(schedule.BeRightBack), "status Be right back"},
		{nil, "hidden+minimised → Reset", nil, ""},
	}
	for _, s := range steps {
		if s.setup != nil {
			s.setup()
		}
		hid0, min0, front0 := teamsState()
		start := time.Now()
		d, err := Open(config.TargetDesktop)
		if err != nil {
			t.Fatalf("%s: open: %v", s.label, err)
		}
		detail, err := Apply(d, s.p, DefaultLabels())
		d.Close()
		if err != nil {
			t.Fatalf("%s: %v", s.label, err)
		}
		hid1, min1, front1 := teamsState()
		got := avatar(t)
		t.Logf("%s (%.1fs): %s\n    avatar: %s | hidden %v→%v minimised %v→%v front %d→%d",
			s.label, time.Since(start).Seconds(), detail, got, hid0, hid1, min0, min1, front0, front1)
		if s.want != "" && !regexp.MustCompile(`(?i)`+s.want).MatchString(got) {
			t.Errorf("%s: avatar says %q", s.label, got)
		}
		// A hidden+minimised window comes back hidden (still invisible) but not minimised.
		if hid0 != hid1 || (!hid0 && min0 != min1) || front0 != front1 {
			t.Errorf("%s: window state not restored", s.label)
		}
	}
}
