//go:build windows

package graph

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// DPAPI: encrypted for the current Windows user.

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func dpapi(in []byte, encrypt bool) ([]byte, error) {
	var out windows.DataBlob
	var err error
	if encrypt {
		err = windows.CryptProtectData(blob(in), nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(blob(in), nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func protect(plain []byte) ([]byte, error)  { return dpapi(plain, true) }
func unprotect(data []byte) ([]byte, error) { return dpapi(data, false) }
