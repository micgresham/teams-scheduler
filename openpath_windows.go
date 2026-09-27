package main

import "os/exec"

// openPath opens a file or folder with its default app.
func openPath(path string) error { return exec.Command("explorer", path).Start() }
