package pairing

import "testing"

func TestAllowAttemptRateLimitsOneSource(t *testing.T) {
	server := &Server{attempts: make(map[string]pairAttempts)}
	for range maxPairAttempts {
		if !server.allowAttempt("100.64.0.1:7420") {
			t.Fatal("allowAttempt rejected a retry inside the configured limit")
		}
	}
	if server.allowAttempt("100.64.0.1:7420") {
		t.Fatal("allowAttempt accepted a request beyond the configured limit")
	}
	if !server.allowAttempt("100.64.0.2:7420") {
		t.Fatal("allowAttempt rate-limited a different source")
	}
}
