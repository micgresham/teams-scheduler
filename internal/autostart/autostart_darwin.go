//go:build darwin

// Package autostart registers the app to launch at login.
package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

const label = "com.teamsstatusscheduler.app"

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

func Supported() bool { return true }

func Enabled() bool {
	_, err := os.Stat(plistPath())
	return err == nil
}

// Set writes or removes a LaunchAgent that starts the app hidden at login.
func Set(enabled bool) error {
	if !enabled {
		err := os.Remove(plistPath())
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key><array><string>%s</string><string>--hidden</string></array>
	<key>RunAtLoad</key><true/>
	<key>ProcessType</key><string>Interactive</string>
</dict>
</plist>
`, label, xmlEscape(exe))
	if err := os.MkdirAll(filepath.Dir(plistPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(plistPath(), []byte(plist), 0o644)
}

func xmlEscape(s string) string {
	r := ""
	for _, c := range s {
		switch c {
		case '&':
			r += "&amp;"
		case '<':
			r += "&lt;"
		case '>':
			r += "&gt;"
		default:
			r += string(c)
		}
	}
	return r
}
