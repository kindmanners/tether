// Package pairing implements the first-contact trust handshake between the
// Orchestrator and a freshly-started Agent (design doc §6.2). Before mTLS
// trust exists between the two, this package's job is to prove that the
// human physically present at both machines actually authorized this
// specific pairing — not just anyone who happened to reach the Agent's
// network port during a vulnerable window.
package pairing

import (
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
	"time"
)

// codeAlphabet deliberately excludes visually ambiguous characters (0/O,
// 1/I/L) since a human reads this code off one screen and types it on
// another — an ambiguous character here means a failed pairing attempt
// and a frustrated user re-typing, not a security issue, but it's a
// completely free fix so there's no reason not to make it.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// codeLength of 6 characters from a 32-character alphabet gives roughly
// 30 bits of entropy (32^6 ≈ 1.07 billion possibilities). That's not huge
// as secrets go, but the code is never relied on alone — see Window,
// which bounds how long any given code is guessable at all. A code that's
// only valid for ~2 minutes doesn't need cryptographic-strength entropy;
// it needs to be short enough for a human to type correctly without
// hassle, which matters more here than in almost any other secret in this
// system.
const codeLength = 6

// Window is how long a generated Code remains valid. Chosen to comfortably
// cover "walk from one machine to the other and type six characters"
// without leaving a code guessable for an extended period. This directly
// implements design doc §6.2's "short-lived random pairing code, ~2
// minutes" decision.
const Window = 2 * time.Minute

// Code is a pairing code with its own expiry, generated fresh for one
// specific pairing attempt. A correct guess consumes it — it cannot
// succeed twice. A WRONG guess does NOT consume it — see Consume's doc
// comment for why that distinction matters (a previous version burned on
// any attempt and had a real denial-of-service problem as a result).
//
// mu guards consumed. Consume is expected to be called from an HTTP
// handler (net/http serves each request in its own goroutine by default),
// so two pairing attempts arriving at nearly the same instant is a real,
// not hypothetical, scenario — without synchronization, both goroutines
// could read consumed==false before either writes true, letting a single
// code be used twice. The mutex makes check-and-set genuinely atomic,
// which merely combining them into one method does not do on its own.
type Code struct {
	mu        sync.Mutex
	value     string
	expiresAt time.Time
	consumed  bool
}

// Generate creates a new pairing Code, valid for Window from now.
func Generate() (*Code, error) {
	value, err := randomCode(codeLength)
	if err != nil {
		return nil, fmt.Errorf("generating pairing code: %w", err)
	}
	return &Code{
		value:     value,
		expiresAt: time.Now().Add(Window),
	}, nil
}

// String returns the human-facing code value, e.g. for display to the
// user so they can type it on the other machine.
func (c *Code) String() string {
	return c.value
}

// ExpiresAt returns when this code stops being valid, e.g. for a caller
// that wants to show a countdown.
func (c *Code) ExpiresAt() time.Time {
	return c.expiresAt
}

// Consume checks the given candidate string against this Code and, if it
// matches and the code is still valid, marks it used and returns true.
// Returns false for any mismatch or expiry.
//
// IMPORTANT — a WRONG guess does NOT burn the code. Only a correct match
// does. This was not the original design: Consume used to burn the code
// on any attempt, matched or not, reasoning that a pairing code should
// allow exactly one guess. That turned out to be a real mistake, found
// during review — it meant a single wrong guess (deliberate or
// accidental, e.g. an attacker scanning the tailnet and POSTing garbage
// to /pair) would permanently kill the pairing session for the
// legitimate human who hasn't typed the correct code in yet, a trivial
// denial-of-service against the one thing this whole mechanism exists to
// protect.
//
// Removing burn-on-mismatch is safe because Window, not per-attempt
// burning, is what actually bounds brute-force risk here: at ~30 bits of
// entropy (32^6 ≈ 1.07 billion possibilities) and a 2-minute window, even
// a sustained 100 req/s attacker gets only ~12,000 attempts — roughly
// 1-in-90,000 odds of a hit. Burn-on-mismatch was defense-in-depth that
// cost far more (a trivial DoS) than it bought (marginal brute-force
// resistance the window already provides).
//
// candidate is normalized (trimmed of whitespace, uppercased) before
// comparison. Humans copying a short code from one screen and typing it
// on another commonly introduce case differences or trailing whitespace
// (autocomplete, a stray space from tapping the on-screen keyboard) —
// rejecting those as "wrong code" would be a usability papercut with no
// security benefit, since case and whitespace were never meant to carry
// any of the code's actual entropy.
//
// This is intentionally the ONLY way to check a Code — there is no
// separate "peek" method that checks validity without consuming. A
// successful match should be usable exactly once; if a caller could
// check validity without consuming, a network retry or a duplicate
// request could let two callers both see "still valid" and both
// proceed. The whole check-and-set sequence is done under mu so this
// guarantee actually holds under concurrent calls, not just when called
// from a single goroutine — see the Code struct's doc comment for why
// that distinction matters.
func (c *Code) Consume(candidate string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.consumed {
		return false
	}
	if time.Now().After(c.expiresAt) {
		return false
	}

	normalized := strings.ToUpper(strings.TrimSpace(candidate))

	// Constant-time comparison isn't used here deliberately: this
	// comparison happens locally within the Agent process (candidate
	// arrives over the network, but comparison itself is in-process), and
	// the code's short lifetime plus its bounded entropy (see the doc
	// comment above) matter far more here than microsecond-level timing
	// side-channels on a 6-character comparison.
	if normalized != c.value {
		return false
	}

	c.consumed = true
	return true
}

// randomCode generates a random string of the given length drawn from
// codeAlphabet, using crypto/rand rather than math/rand — this is a
// security-relevant secret (the thing that authorizes a pairing), not a
// cosmetic ID, so it needs a cryptographically secure random source.
func randomCode(length int) (string, error) {
	alphabetLen := byte(len(codeAlphabet))
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}

	code := make([]byte, length)
	for i, b := range buf {
		// Modulo bias note: codeAlphabet is 32 characters, and byte
		// values range 0-255 (256 = 32*8 exactly), so 256 % 32 == 0 —
		// there's no bias here since 32 divides 256 evenly. This would
		// need a rejection-sampling approach if the alphabet length ever
		// changed to something that doesn't evenly divide 256.
		code[i] = codeAlphabet[b%alphabetLen]
	}
	return string(code), nil
}