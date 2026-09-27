package main

import "os/exec"

// openPath opens a file or folder with its default app (text files in TextEdit etc.).
func openPath(path string) error { return exec.Command("open", path).Start() }

// openAccessibilitySettings opens System Settings → Privacy & Security → Accessibility.
func openAccessibilitySettings() error {
	return exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility").Start()
}
