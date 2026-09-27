//go:build !darwin && !windows

package keepawake

type noop struct{}

func newPlatform() platform                 { return noop{} }
func (noop) set(active, display bool) error { return nil }
