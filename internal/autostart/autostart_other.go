//go:build !darwin && !windows

package autostart

func Supported() bool        { return false }
func Enabled() bool          { return false }
func Set(enabled bool) error { return nil }
