package utils

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// GenerateRandomPassword creates a cryptographically secure random password.
// It fails HARD if the OS entropy pool is unreadable.
// TODO: I still need to check for something better
func GenerateRandomPassword(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("critical security failure: cannot read from crypto/rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}
