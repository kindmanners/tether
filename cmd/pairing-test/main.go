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

// Command pairing-test is a throwaway harness to verify internal/pairing's
// Code type, including a deliberate concurrency test proving Consume() is
// actually safe under concurrent calls (not just correct when called from
// a single goroutine). Delete once the real pairing HTTP handlers (§6.2)
// exist and exercise this code for real.
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"tether/internal/pairing"
)

func main() {
	fmt.Println("=== Test 1: correct code is accepted ===")
	code, err := pairing.Generate()
	if err != nil {
		panic(err)
	}
	fmt.Printf("  generated code: %s (expires %s)\n", code.String(), code.ExpiresAt().Format("15:04:05"))
	if code.Consume(code.String()) {
		fmt.Println("  PASS: correct code accepted")
	} else {
		fmt.Println("  FAIL: correct code was rejected")
	}

	fmt.Println()
	fmt.Println("=== Test 2: same code cannot be consumed twice ===")
	if code.Consume(code.String()) {
		fmt.Println("  FAIL: code was accepted a second time — single-use is broken")
	} else {
		fmt.Println("  PASS: second attempt correctly rejected")
	}

	fmt.Println()
	fmt.Println("=== Test 3: wrong code is rejected, but does NOT burn the code ===")
	// This behavior flipped after review: an earlier version burned the
	// code on ANY attempt, matched or not. That created a trivial
	// denial-of-service — anyone who could reach the endpoint (not just
	// the legitimate user) could kill a pairing session with one wrong
	// guess, before the real user ever got to type the correct code.
	// This test now asserts the FIXED behavior: a wrong guess is
	// rejected, but the code remains usable afterward.
	code2, _ := pairing.Generate()
	if code2.Consume("WRONGCODE") {
		fmt.Println("  FAIL: wrong code was accepted")
	} else {
		fmt.Println("  PASS: wrong code correctly rejected")
	}
	if code2.Consume(code2.String()) {
		fmt.Println("  PASS: correct code still works AFTER a wrong guess — DoS fix confirmed")
	} else {
		fmt.Println("  FAIL: correct code was rejected after a prior wrong guess — DoS vulnerability is back")
	}

	fmt.Println()
	fmt.Println("=== Test 4: case and whitespace normalization ===")
	code3, _ := pairing.Generate()
	messy := "  " + toLowerASCII(code3.String()) + "  "
	if code3.Consume(messy) {
		fmt.Printf("  PASS: %q (lowercase + whitespace) accepted for code %s\n", messy, code3.String())
	} else {
		fmt.Printf("  FAIL: %q should have matched code %s after normalization\n", messy, code3.String())
	}

	fmt.Println()
	fmt.Println("=== Test 5: concurrent Consume() calls — only ONE should ever succeed ===")
	// This is the real test for the race-condition fix. We fire many
	// goroutines at the exact same Code simultaneously, all guessing the
	// correct value. Before the mutex fix, it was possible for more than
	// one goroutine to read consumed==false before any of them wrote
	// true, letting the code be "used" more than once. Run with -race to
	// have Go's own race detector confirm there's no data race at all,
	// not just check the observable outcome.
	code4, _ := pairing.Generate()
	const attempts = 200
	var successCount int64
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			if code4.Consume(code4.String()) {
				atomic.AddInt64(&successCount, 1)
			}
		}()
	}
	wg.Wait()
	fmt.Printf("  %d concurrent goroutines attempted the same correct code\n", attempts)
	fmt.Printf("  successful consumptions: %d\n", successCount)
	if successCount == 1 {
		fmt.Println("  PASS: exactly one goroutine won — single-use holds under concurrency")
	} else {
		fmt.Printf("  FAIL: expected exactly 1 success, got %d — the race condition is NOT fixed\n", successCount)
	}

	fmt.Println()
	fmt.Println("=== Test 6: expiry ===")
	// pairing.Window is 2 minutes — too long to actually wait out in a
	// quick manual test. This just confirms ExpiresAt() reports a
	// sensible value; a real expiry test belongs in a proper Go test file
	// using a fake clock, not this harness.
	code5, _ := pairing.Generate()
	remaining := time.Until(code5.ExpiresAt())
	fmt.Printf("  code expires in %s (should be ~2m0s)\n", remaining.Round(time.Second))

	fmt.Println()
	fmt.Println("=== Test 7: concrete DoS-fix proof — many wrong guesses cannot block one correct one ===")
	// Directly simulates the scenario the finding described: an attacker
	// (or many) hammering the endpoint with wrong guesses, concurrently
	// with the legitimate user's correct attempt arriving at some
	// unpredictable point in the middle. Before the fix, ANY one of the
	// wrong guesses (even the very first) would have permanently killed
	// the code. After the fix, the correct guess should succeed
	// regardless of how many wrong guesses surround it.
	code6, _ := pairing.Generate()
	const wrongAttempts = 100
	var wg2 sync.WaitGroup
	var wrongSuccesses int64
	wg2.Add(wrongAttempts + 1)
	for i := 0; i < wrongAttempts; i++ {
		go func(n int) {
			defer wg2.Done()
			if code6.Consume(fmt.Sprintf("WRONG%d", n)) {
				atomic.AddInt64(&wrongSuccesses, 1)
			}
		}(i)
	}
	var legitimateSucceeded int64
	go func() {
		defer wg2.Done()
		if code6.Consume(code6.String()) {
			atomic.AddInt64(&legitimateSucceeded, 1)
		}
	}()
	wg2.Wait()
	fmt.Printf("  %d concurrent wrong guesses + 1 correct guess, all racing\n", wrongAttempts)
	fmt.Printf("  wrong guesses that incorrectly succeeded: %d (must be 0)\n", wrongSuccesses)
	fmt.Printf("  legitimate correct guess succeeded: %v (must be true)\n", legitimateSucceeded == 1)
	if wrongSuccesses == 0 && legitimateSucceeded == 1 {
		fmt.Println("  PASS: DoS fix holds even under concurrent attacker + legitimate traffic")
	} else {
		fmt.Println("  FAIL: either a wrong guess succeeded, or the legitimate guess was blocked")
	}
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}