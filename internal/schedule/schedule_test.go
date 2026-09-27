package schedule

import (
	"testing"
	"time"
)

var weekdays = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
var allDays = []time.Weekday{0, 1, 2, 3, 4, 5, 6}

// 2026-09-28 is a Monday.
func at(day int, hhmm string) time.Time {
	m, _ := parseHHMM(hhmm)
	return time.Date(2026, 9, day, m/60, m%60, 0, 0, time.Local)
}

func workWeek(prio int) Schedule {
	return Schedule{ID: "work", Name: "Work", Priority: prio, Enabled: true, Blocks: []Block{
		{weekdays, "09:00", "12:00", Available},
		{weekdays, "12:00", "13:00", Away},
		{weekdays, "13:00", "17:00", Available},
	}}
}

func presence(r *Resolution) Presence {
	if r == nil {
		return ""
	}
	return r.Presence
}

func TestBasicBlocks(t *testing.T) {
	s := []Schedule{workWeek(10)}
	cases := []struct {
		when time.Time
		want Presence
	}{
		{at(28, "08:59"), ""},
		{at(28, "09:00"), Available},
		{at(28, "12:30"), Away},
		{at(28, "16:59"), Available},
		{at(28, "17:00"), ""},                                 // end is exclusive
		{time.Date(2026, 10, 3, 10, 0, 0, 0, time.Local), ""}, // Saturday
	}
	for _, c := range cases {
		if got := presence(Resolve(s, c.when)); got != c.want {
			t.Errorf("%v: got %q want %q", c.when, got, c.want)
		}
	}
	if r := Resolve(s, at(28, "10:00")); !r.BlockEnd.Equal(at(28, "12:00")) {
		t.Errorf("block end %v", r.BlockEnd)
	}
}

func TestOvernight(t *testing.T) {
	s := []Schedule{{ID: "n", Name: "Night", Priority: 1, Enabled: true, Blocks: []Block{
		{[]time.Weekday{time.Friday}, "22:00", "06:00", Offline},
	}}}
	fri := func(h string) time.Time { return time.Date(2026, 10, 2, 0, 0, 0, 0, time.Local).Add(mins(h)) }
	sat := func(h string) time.Time { return time.Date(2026, 10, 3, 0, 0, 0, 0, time.Local).Add(mins(h)) }
	if presence(Resolve(s, fri("23:00"))) != Offline || presence(Resolve(s, sat("05:59"))) != Offline {
		t.Error("overnight block not active")
	}
	if Resolve(s, sat("06:00")) != nil || Resolve(s, fri("05:00")) != nil {
		t.Error("overnight block active outside its window")
	}
}

func mins(hhmm string) time.Duration { m, _ := parseHHMM(hhmm); return time.Duration(m) * time.Minute }

func TestPriorityLowerNumberWins(t *testing.T) {
	focus := Schedule{ID: "f", Name: "Focus", Priority: 1, Enabled: true, Blocks: []Block{
		{[]time.Weekday{time.Monday}, "10:00", "11:00", DoNotDisturb},
	}}
	s := []Schedule{workWeek(10), focus}
	if presence(Resolve(s, at(28, "10:30"))) != DoNotDisturb {
		t.Error("priority 1 should win")
	}
	if presence(Resolve(s, at(28, "11:00"))) != Available {
		t.Error("falls back after focus block")
	}
	s[1].Priority = 20
	if presence(Resolve(s, at(28, "10:30"))) != Available {
		t.Error("priority 10 should win over 20")
	}
	s[0].Enabled = false
	if presence(Resolve(s, at(28, "10:30"))) != DoNotDisturb {
		t.Error("disabled schedule should be ignored")
	}
}

func TestDateRange(t *testing.T) {
	vacation := Schedule{ID: "v", Name: "Vacation", Priority: 1, Enabled: true,
		ValidFrom: "2026-09-29", ValidUntil: "2026-09-30",
		Blocks: []Block{{allDays, "00:00", "00:00", Offline}}}
	s := []Schedule{workWeek(10), vacation}
	for when, want := range map[time.Time]Presence{
		at(28, "10:00"): Available,
		at(29, "10:00"): Offline,
		at(30, "23:59"): Offline,
		time.Date(2026, 10, 1, 10, 0, 0, 0, time.Local): Available,
	} {
		if got := presence(Resolve(s, when)); got != want {
			t.Errorf("%v: got %q want %q", when, got, want)
		}
	}
}

func TestNextChange(t *testing.T) {
	s := []Schedule{workWeek(10)}
	when, r, ok := NextChange(s, at(28, "10:00"))
	if !ok || !when.Equal(at(28, "12:00")) || r.Presence != Away {
		t.Errorf("got %v %v", when, r)
	}
	when, r, ok = NextChange(s, at(28, "16:00"))
	if !ok || !when.Equal(at(28, "17:00")) || r != nil {
		t.Errorf("got %v %v", when, r)
	}
	when, _, _ = NextChange(s, time.Date(2026, 10, 2, 18, 0, 0, 0, time.Local)) // Friday evening
	if !when.Equal(time.Date(2026, 10, 5, 9, 0, 0, 0, time.Local)) {
		t.Errorf("expected Monday 09:00, got %v", when)
	}
}

func TestNextChangeSkipsSamePresence(t *testing.T) {
	s := []Schedule{{ID: "a", Name: "A", Priority: 1, Enabled: true, Blocks: []Block{
		{[]time.Weekday{time.Monday}, "09:00", "10:00", Busy},
		{[]time.Weekday{time.Monday}, "10:00", "11:00", Busy},
	}}}
	when, r, _ := NextChange(s, at(28, "09:30"))
	if !when.Equal(at(28, "11:00")) || r != nil {
		t.Errorf("got %v %v", when, r)
	}
}

func TestValidate(t *testing.T) {
	good := workWeek(10)
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := Schedule{Name: " ", Priority: 0, Blocks: []Block{{nil, "25:00", "9", "Nope"}}, ValidFrom: "2026-10-02", ValidUntil: "2026-10-01"}
	if err := bad.Validate(); err == nil {
		t.Fatal("expected errors")
	}
}
