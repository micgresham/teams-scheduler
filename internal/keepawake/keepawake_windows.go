//go:build windows

package keepawake

import (
	"errors"
	"runtime"

	"golang.org/x/sys/windows"
)

const (
	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
)

var procSetThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// Windows: SetThreadExecutionState is per-thread, so a dedicated goroutine
// locked to one OS thread owns it for the life of the process.
type win struct{ reqs chan request }

type request struct {
	flags uintptr
	done  chan error
}

func newPlatform() platform {
	w := &win{reqs: make(chan request)}
	go func() {
		runtime.LockOSThread()
		for r := range w.reqs {
			ret, _, _ := procSetThreadExecutionState.Call(r.flags)
			if ret == 0 {
				r.done <- errors.New("SetThreadExecutionState failed")
			} else {
				r.done <- nil
			}
		}
	}()
	return w
}

func (w *win) set(active, display bool) error {
	flags := uintptr(esContinuous)
	if active {
		flags |= esSystemRequired
		if display {
			flags |= esDisplayRequired
		}
	}
	done := make(chan error)
	w.reqs <- request{flags, done}
	return <-done
}
