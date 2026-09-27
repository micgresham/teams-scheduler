// Package keepawake stops the computer idle-sleeping (e.g. so SSH sessions
// stay connected). It is independent of the Teams status logic.
package keepawake

import "sync"

// Controller turns sleep prevention on and off; Set is idempotent.
type Controller struct {
	mu      sync.Mutex
	active  bool
	display bool
	impl    platform
}

func New() *Controller { return &Controller{impl: newPlatform()} }

// Set enables (active) or disables sleep prevention; display also keeps the screen on.
func (c *Controller) Set(active, display bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	display = display && active
	if active == c.active && display == c.display {
		return nil
	}
	if err := c.impl.set(active, display); err != nil {
		return err
	}
	c.active, c.display = active, display
	return nil
}

func (c *Controller) Active() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

type platform interface {
	set(active, display bool) error
}
