package broker

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/interactionlabs/devtools/control-room/internal/statedir"
)

// secretBytes is the length of the control secret in raw bytes (256-bit).
const secretBytes = 32

// LoadOrCreateSecret returns the persisted control secret at path, creating it
// with a fresh 256-bit random value (mode 0600) if it does not exist.
//
// The secret authenticates control-channel callers. It is defense in depth on
// top of the private (0700) parent directory: a process that cannot read the
// 0600 secret file cannot forge a request even if it can somehow reach the
// socket. The returned string is the hex-encoded secret.
func LoadOrCreateSecret(path string) (string, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		s := strings.TrimSpace(string(data))
		if len(s) < secretBytes*2 {
			return "", fmt.Errorf("broker: control secret at %q is too short; refusing to use", path)
		}
		// Re-assert restrictive permissions in case they drifted.
		if err := os.Chmod(path, statedir.SecretPerm); err != nil {
			return "", fmt.Errorf("broker: securing control secret: %w", err)
		}
		return s, nil
	case os.IsNotExist(err):
		return createSecret(path)
	default:
		return "", fmt.Errorf("broker: reading control secret: %w", err)
	}
}

func createSecret(path string) (string, error) {
	var b [secretBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("broker: generating control secret: %w", err)
	}
	s := hex.EncodeToString(b[:])
	// O_EXCL so a concurrent creator cannot be silently overwritten; 0600 from
	// creation so the secret is never briefly readable by others.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, statedir.SecretPerm)
	if err != nil {
		if os.IsExist(err) {
			// Lost a race; read the winner's secret.
			return LoadOrCreateSecret(path)
		}
		return "", fmt.Errorf("broker: creating control secret: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		return "", fmt.Errorf("broker: writing control secret: %w", err)
	}
	return s, nil
}

// secretsEqual compares two secrets in constant time to avoid leaking match
// progress through timing.
func secretsEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
