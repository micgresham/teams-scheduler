//go:build darwin

package keepawake

import (
	"os/exec"
	"strings"
	"testing"
)

func assertions(t *testing.T) string {
	out, err := exec.Command("pmset", "-g", "assertions").Output()
	if err != nil {
		t.Skip("pmset unavailable")
	}
	return string(out)
}

func TestAssertionsTakenAndReleased(t *testing.T) {
	c := New()
	if err := c.Set(true, false); err != nil {
		t.Fatal(err)
	}
	a := assertions(t)
	if !strings.Contains(a, "Teams Status Scheduler: keep awake") || !strings.Contains(a, "PreventUserIdleSystemSleep") {
		t.Fatalf("assertion not registered:\n%s", a)
	}
	if err := c.Set(false, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(assertions(t), "Teams Status Scheduler: keep awake") {
		t.Fatal("assertion not released")
	}
}
