// Package schedule holds the schedule model and the pure logic that decides
// which Teams status should be active at a given moment.
package schedule

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Presence is a status the user can pick in Teams.
type Presence string

const (
	Available    Presence = "Available"
	Busy         Presence = "Busy"
	DoNotDisturb Presence = "DoNotDisturb"
	BeRightBack  Presence = "BeRightBack"
	Away         Presence = "Away"
	Offline      Presence = "Offline"
)

// AllPresences in menu order.
var AllPresences = []Presence{Available, Busy, DoNotDisturb, BeRightBack, Away, Offline}

// Label is the text Teams shows for the status.
func (p Presence) Label() string {
	switch p {
	case Available:
		return "Available"
	case Busy:
		return "Busy"
	case DoNotDisturb:
		return "Do not disturb"
	case BeRightBack:
		return "Be right back"
	case Away:
		return "Appear away"
	case Offline:
		return "Appear offline"
	}
	return string(p)
}

// Valid reports whether p is a known presence.
func (p Presence) Valid() bool {
	for _, q := range AllPresences {
		if p == q {
			return true
		}
	}
	return false
}

// Date is a calendar day serialised as "YYYY-MM-DD" (empty = unset).
type Date string

func (d Date) parse() (time.Time, bool) {
	if d == "" {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02", string(d), time.Local)
	return t, err == nil
}

// Block is a recurring window on selected weekdays. Days use time.Weekday
// numbering (0 = Sunday). If End <= Start the block runs overnight into the
// next day; Start == End covers the full 24 hours.
type Block struct {
	Days     []time.Weekday `json:"days"`
	Start    string         `json:"start"` // "HH:MM"
	End      string         `json:"end"`   // "HH:MM"
	Presence Presence       `json:"presence"`
}

// Schedule is a named set of blocks. Lower Priority numbers win (1 = highest).
type Schedule struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Priority   int     `json:"priority"`
	Enabled    bool    `json:"enabled"`
	ValidFrom  Date    `json:"validFrom,omitempty"`
	ValidUntil Date    `json:"validUntil,omitempty"`
	Blocks     []Block `json:"blocks"`
}

// Resolution is the winning status at a moment.
type Resolution struct {
	Presence Presence
	Schedule *Schedule
	Block    *Block
	BlockEnd time.Time
}

func parseHHMM(s string) (int, error) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid time %q (want HH:MM)", s)
	}
	return h*60 + m, nil
}

func atMinutes(day time.Time, minutes int) time.Time {
	y, mo, d := day.Date()
	return time.Date(y, mo, d, minutes/60, minutes%60, 0, 0, day.Location())
}

func midnight(t time.Time) time.Time { return atMinutes(t, 0) }

func (b *Block) hasDay(d time.Weekday) bool {
	for _, x := range b.Days {
		if x == d {
			return true
		}
	}
	return false
}

// occurrence returns the start/end of the block occurrence beginning on day.
func (b *Block) occurrence(day time.Time) (time.Time, time.Time, bool) {
	s, err1 := parseHHMM(b.Start)
	e, err2 := parseHHMM(b.End)
	if err1 != nil || err2 != nil || !b.hasDay(day.Weekday()) {
		return time.Time{}, time.Time{}, false
	}
	start := atMinutes(day, s)
	end := atMinutes(day, e)
	if e <= s {
		end = atMinutes(day.AddDate(0, 0, 1), e)
	}
	return start, end, true
}

// window returns the occurrence of b containing now, if any.
func (b *Block) window(now time.Time) (time.Time, time.Time, bool) {
	for offset := 0; offset <= 1; offset++ { // started today, or (overnight) yesterday
		day := midnight(now).AddDate(0, 0, -offset)
		if start, end, ok := b.occurrence(day); ok && !now.Before(start) && now.Before(end) {
			return start, end, true
		}
	}
	return time.Time{}, time.Time{}, false
}

// ValidOn reports whether the schedule's date range includes day.
func (s *Schedule) ValidOn(day time.Time) bool {
	day = midnight(day)
	if from, ok := s.ValidFrom.parse(); ok && day.Before(from) {
		return false
	}
	if until, ok := s.ValidUntil.parse(); ok && day.After(until) {
		return false
	}
	return true
}

// Resolve returns the winning presence at now, or nil if no schedule applies.
// Ties on priority go to the schedule listed first; within a schedule the
// first matching block wins.
func Resolve(schedules []Schedule, now time.Time) *Resolution {
	var best *Resolution
	bestPrio := 0
	for i := range schedules {
		s := &schedules[i]
		if !s.Enabled {
			continue
		}
		for j := range s.Blocks {
			b := &s.Blocks[j]
			start, end, ok := b.window(now)
			if !ok || !s.ValidOn(start) { // date range applies to the day the occurrence started
				continue
			}
			if best == nil || s.Priority < bestPrio {
				best = &Resolution{Presence: b.Presence, Schedule: s, Block: b, BlockEnd: end}
				bestPrio = s.Priority
			}
			break
		}
	}
	return best
}

func key(r *Resolution) string {
	if r == nil {
		return ""
	}
	return string(r.Presence) + "|" + r.Schedule.ID
}

// NextChange finds the next instant (within 8 days) where the resolved
// presence or winning schedule differs from now's.
func NextChange(schedules []Schedule, now time.Time) (time.Time, *Resolution, bool) {
	current := key(Resolve(schedules, now))
	until := now.AddDate(0, 0, 8)
	seen := map[time.Time]bool{}
	var points []time.Time
	add := func(t time.Time) {
		if t.After(now) && !t.After(until) && !seen[t] {
			seen[t] = true
			points = append(points, t)
		}
	}
	for day := midnight(now).AddDate(0, 0, -1); !day.After(until); day = day.AddDate(0, 0, 1) {
		add(day) // date-range edges fall on midnight
		for i := range schedules {
			if !schedules[i].Enabled {
				continue
			}
			for j := range schedules[i].Blocks {
				if start, end, ok := schedules[i].Blocks[j].occurrence(day); ok {
					add(start)
					add(end)
				}
			}
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Before(points[j]) })
	for _, p := range points {
		if r := Resolve(schedules, p); key(r) != current {
			return p, r, true
		}
	}
	return time.Time{}, nil, false
}

// Validate checks a schedule before it is saved.
func (s *Schedule) Validate() error {
	var problems []string
	if strings.TrimSpace(s.Name) == "" {
		problems = append(problems, "give the schedule a name")
	}
	if s.Priority < 1 || s.Priority > 99 {
		problems = append(problems, "priority must be 1–99")
	}
	if len(s.Blocks) == 0 {
		problems = append(problems, "add at least one time block")
	}
	for i, b := range s.Blocks {
		if len(b.Days) == 0 {
			problems = append(problems, fmt.Sprintf("block %d needs at least one day", i+1))
		}
		if _, err := parseHHMM(b.Start); err != nil {
			problems = append(problems, fmt.Sprintf("block %d: %v", i+1, err))
		}
		if _, err := parseHHMM(b.End); err != nil {
			problems = append(problems, fmt.Sprintf("block %d: %v", i+1, err))
		}
		if !b.Presence.Valid() {
			problems = append(problems, fmt.Sprintf("block %d: unknown status %q", i+1, b.Presence))
		}
	}
	from, okF := s.ValidFrom.parse()
	until, okU := s.ValidUntil.parse()
	if s.ValidFrom != "" && !okF || s.ValidUntil != "" && !okU {
		problems = append(problems, "dates must be YYYY-MM-DD")
	} else if okF && okU && until.Before(from) {
		problems = append(problems, "the 'until' date is before the 'from' date")
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}
