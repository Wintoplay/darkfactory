package id

import (
	"crypto/rand"
	"encoding/hex"
)

// New returns a 128-bit random identifier.
func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
