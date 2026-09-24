// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

// Package pairing implements the first-contact trust handshake between the
// Orchestrator and a freshly-started Agent.
package pairing

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"time"
)

// codeAlphabet excludes visually ambiguous characters (0/O, 1/I/L).
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// codeLength gives an out-of-band pairing secret 60 bits of entropy. Pairing
// proofs are observable by a network attacker, so six characters would permit
// practical offline guessing even during a short pairing window.
const codeLength = 12

const pairingProofDomain = "tether/pairing-proof/v1"

// Window is how long a generated Code remains valid.
const Window = 2 * time.Minute

// Code is a pairing secret generated for exactly one pairing attempt. The
// secret is displayed out of band and is never sent over the network. Instead,
// it authenticates HMAC proofs that bind the Agent and Orchestrator
// certificates to the pairing transaction.
type Code struct {
	mu        sync.Mutex
	value     string
	expiresAt time.Time
	consumed  bool
}

// Generate creates a new pairing Code, valid for Window from now.
func Generate() (*Code, error) {
	return GenerateWithWindow(Window)
}

// GenerateWithWindow creates a new pairing Code valid for the given duration.
func GenerateWithWindow(window time.Duration) (*Code, error) {
	value, err := randomCode(codeLength)
	if err != nil {
		return nil, fmt.Errorf("generating pairing code: %w", err)
	}
	return &Code{value: value, expiresAt: time.Now().Add(window)}, nil
}

// String returns the human-facing pairing secret for local display.
func (c *Code) String() string {
	return c.value
}

// ExpiresAt returns when this code stops being valid.
func (c *Code) ExpiresAt() time.Time {
	return c.expiresAt
}

// proof creates an HMAC over a domain-separated, length-prefixed binding
// sequence. Length prefixes avoid ambiguous encodings as the protocol evolves.
func proof(code string, bindings ...[]byte) []byte {
	mac := hmac.New(sha256.New, []byte(normalizeCode(code)))
	_, _ = mac.Write([]byte(pairingProofDomain))
	for _, binding := range bindings {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(binding)))
		_, _ = mac.Write(length[:])
		_, _ = mac.Write(binding)
	}
	return mac.Sum(nil)
}

func normalizeCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
}

func encodeProof(code string, bindings ...[]byte) string {
	return base64.RawStdEncoding.EncodeToString(proof(code, bindings...))
}

func validProof(code, encoded string, bindings ...[]byte) bool {
	received, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(received, proof(code, bindings...)) == 1
}

// Proof creates a proof for bootstrap information without consuming the code.
// The Client verifies this proof before it trusts the Agent certificate.
func (c *Code) Proof(bindings ...[]byte) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return encodeProof(c.value, bindings...)
}

// ConsumeProof verifies a certificate-bound proof and marks the code consumed
// only on success. Invalid proofs do not end a legitimate pairing session.
func (c *Code) ConsumeProof(encoded string, bindings ...[]byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.consumed || time.Now().After(c.expiresAt) {
		return false
	}
	if !validProof(c.value, encoded, bindings...) {
		return false
	}

	c.consumed = true
	return true
}

// randomCode generates a uniformly distributed code using crypto/rand.
func randomCode(length int) (string, error) {
	alphabetLen := byte(len(codeAlphabet))
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}

	code := make([]byte, length)
	for i, b := range buf {
		// 256 divides evenly by the 32-character alphabet, so this mapping
		// introduces no modulo bias.
		code[i] = codeAlphabet[b%alphabetLen]
	}
	return string(code), nil
}
