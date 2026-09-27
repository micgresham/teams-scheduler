//go:build darwin && realteams

package teams

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"teamsstatus/internal/config"
)

func visible(t *testing.T) string {
	out, err := exec.Command("osascript", "-e", `tell application "System Events" to get visible of process "MSTeams"`).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestRealHiddenRestored(t *testing.T) {
	_ = exec.Command("osascript", "-e", `tell application "System Events" to set visible of process "MSTeams" to false`).Run()
	time.Sleep(time.Second)
	before := visible(t)
	d, err := Open(config.TargetDesktop)
	if err != nil {
		t.Fatal(err)
	}
	during := visible(t)
	d.Close()
	time.Sleep(time.Second)
	after := visible(t)
	t.Logf("visible before=%s during=%s after=%s", before, during, after)
	if before != "false" || after != "false" {
		t.Error("Teams should be hidden again after Close")
	}
}
