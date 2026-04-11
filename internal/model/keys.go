package model

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

var nonKeyCharRe = regexp.MustCompile(`[^a-zA-Z0-9_#]`)

// EscapeProgram returns the basename of program with non-[a-zA-Z0-9_#] chars
// replaced by +.
func EscapeProgram(program string) string {
	name := filepath.Base(program)
	return nonKeyCharRe.ReplaceAllString(name, "+")
}

// GenerateKey returns a job key of the form {unix_ts}_{random8hex}_{program}.
// Example: 1712912345_a3f1c8d2_make
func GenerateKey(program string) string {
	ts := time.Now().Unix()
	rnd := randomHex(4) // 4 bytes → 8 hex chars
	prog := EscapeProgram(program)
	return fmt.Sprintf("%d_%s_%s", ts, rnd, prog)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("model: failed to generate random bytes: " + err.Error())
	}
	return hex.EncodeToString(b)
}
