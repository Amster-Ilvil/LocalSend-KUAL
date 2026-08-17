package app

import (
	"os"
	"path/filepath"
	"strings"
)

// LoadOrCreateHTTPFingerprint returns a persistent random device identifier for
// plaintext LocalSend v2.2. The protocol explicitly treats the HTTP fingerprint
// as a random identifier rather than a TLS certificate fingerprint. Keeping it
// separate also makes an HTTP compatibility install show up as a fresh device
// instead of reusing a previously cached HTTPS identity.
func LoadOrCreateHTTPFingerprint(root string) (string, error) {
	dir := filepath.Join(root, "state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "http-fingerprint")
	if b, err := os.ReadFile(path); err == nil {
		v := strings.ToUpper(strings.TrimSpace(string(b)))
		if len(v) >= 32 {
			return v, nil
		}
	}
	v, err := randomHex(32)
	if err != nil {
		return "", err
	}
	v = strings.ToUpper(v)
	if err := os.WriteFile(path, []byte(v+"\n"), 0o600); err != nil {
		return "", err
	}
	return v, nil
}
