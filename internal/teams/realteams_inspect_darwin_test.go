//go:build darwin && realteams

package teams

import (
	"testing"

	"teamsstatus/internal/config"
)

// Prints the Inspect output for the installed Teams (doesn't change status).
func TestRealInspect(t *testing.T) {
	d, err := Open(config.TargetDesktop)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	t.Log("\n" + Inspect(d, DefaultLabels()))
}
