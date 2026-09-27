package graph

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

// fileCache persists MSAL's token cache in an encrypted file. Encryption is
// platform specific (protect/unprotect): macOS AES-GCM with the key in the
// Keychain, Windows DPAPI.
type fileCache struct{ path string }

func (f fileCache) Replace(ctx context.Context, c cache.Unmarshaler, _ cache.ReplaceHints) error {
	data, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	plain, err := unprotect(data)
	if err != nil {
		return nil // unreadable (e.g. key lost): start signed out rather than fail
	}
	return c.Unmarshal(plain)
}

func (f fileCache) Export(ctx context.Context, c cache.Marshaler, _ cache.ExportHints) error {
	plain, err := c.Marshal()
	if err != nil {
		return err
	}
	data, err := protect(plain)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

func (f fileCache) clear() { _ = os.Remove(f.path) }
