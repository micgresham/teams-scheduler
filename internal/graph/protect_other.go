//go:build !darwin && !windows

package graph

// Other platforms: the file is only protected by its 0600 permissions.
func protect(plain []byte) ([]byte, error)  { return plain, nil }
func unprotect(data []byte) ([]byte, error) { return data, nil }
