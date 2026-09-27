//go:build darwin

package graph

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"

	"github.com/zalando/go-keyring"
)

const (
	keychainService = "TeamsStatusScheduler"
	keychainAccount = "graph-token-cache-key"
)

// cacheKey returns the AES-256 key kept in the login Keychain, creating it once.
func cacheKey() ([]byte, error) {
	if s, err := keyring.Get(keychainService, keychainAccount); err == nil {
		if k, err := base64.StdEncoding.DecodeString(s); err == nil && len(k) == 32 {
			return k, nil
		}
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := keyring.Set(keychainService, keychainAccount, base64.StdEncoding.EncodeToString(k)); err != nil {
		return nil, err
	}
	return k, nil
}

func gcm() (cipher.AEAD, error) {
	k, err := cacheKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func protect(plain []byte) ([]byte, error) {
	a, err := gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, plain, nil), nil
}

func unprotect(data []byte) ([]byte, error) {
	a, err := gcm()
	if err != nil {
		return nil, err
	}
	if len(data) < a.NonceSize() {
		return nil, errors.New("token cache too short")
	}
	return a.Open(nil, data[:a.NonceSize()], data[a.NonceSize():], nil)
}
