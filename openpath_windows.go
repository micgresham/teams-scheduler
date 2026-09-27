package main

import "os/exec"

// openPath opens a file or folder with its default app.
func openPath(path string) error { return exec.Command("explorer", path).Start() }

// openAccessibilitySettings: Windows needs no permission for UI Automation.
func openAccessibilitySettings() error { return nil }
