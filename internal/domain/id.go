package domain

import (
	"crypto/rand"
	"encoding/hex"
)

func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
