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
	fmt.Println("=== Test 3: wrong code is rejected, and burns the attempt ===")
	code2, _ := pairing.Generate()
	if code2.Consume("WRONGCODE") {
		fmt.Println("  FAIL: wrong code was accepted")
	} else {
		fmt.Println("  PASS: wrong code correctly rejected")
	}
	if code2.Consume(code2.String()) {
		fmt.Println("  FAIL: correct code worked AFTER a wrong guess — should be burned by the failed attempt")
	} else {
		fmt.Println("  PASS: code correctly burned after one wrong guess, even though it hadn't expired")
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