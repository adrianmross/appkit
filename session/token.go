package session

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

// Hash returns the hex-encoded SHA-256 of a token secret. Only the hash is stored.
func Hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// EqualHash reports whether a stored hex hash matches a secret.
func EqualHash(storedHex, secret string) bool {
	want, err := hex.DecodeString(storedHex)
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(want, sum[:]) == 1
}

// Generate returns a new secret that starts with the app's marker (for
// example "pst_"), the non-secret display prefix stored beside the hash (the
// marker and eight characters), and the hash.
func Generate(marker string) (secret, prefix, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", err
	}
	secret = marker + hex.EncodeToString(buf)
	return secret, secret[:len(marker)+8], Hash(secret), nil
}
