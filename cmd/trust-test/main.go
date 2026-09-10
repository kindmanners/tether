// Command trust-test is a throwaway harness to verify internal/trust
// before it's wired into the real pairing handshake (§6.2). Delete once
// the pairing HTTP server exists and exercises this code for real.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"os"
	"time"

	"tether/internal/trust"
)

// makeTestCert builds a minimal self-signed cert with a controllable
// validity window, so we can test expired/not-yet-valid cases without
// waiting around for a real 2-year cert to actually expire.
func makeTestCert(commonName string, notBefore, notAfter time.Time) []byte {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	return der
}

func main() {
	now := time.Now()

	fmt.Println("=== Test 1: Pin + Get round-trip with a valid cert ===")
	validCert := makeTestCert("mathesis", now.Add(-time.Hour), now.Add(24*time.Hour))
	if err := trust.Pin("mathesis", validCert); err != nil {
		fmt.Fprintf(os.Stderr, "  FAIL: Pin failed on valid cert: %v\n", err)
		os.Exit(1)
	}
	peer, found, err := trust.Get("mathesis")
	if err != nil {
		fmt.Printf("  FAIL: Get returned an error for a valid cert: %v\n", err)
	} else if !found {
		fmt.Println("  FAIL: Get did not find the cert we just pinned")
	} else {
		fmt.Printf("  PASS: pinned and retrieved cert for %s, trusted at %s\n", peer.Hostname, peer.TrustedAt.Format(time.RFC3339))
	}

	fmt.Println()
	fmt.Println("=== Test 2: Pin rejects an already-expired cert ===")
	expiredCert := makeTestCert("old-node", now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	if err := trust.Pin("old-node", expiredCert); err != nil {
		fmt.Printf("  PASS: expired cert correctly rejected: %v\n", err)
	} else {
		fmt.Println("  FAIL: an already-expired certificate was accepted by Pin")
	}

	fmt.Println()
	fmt.Println("=== Test 3: Pin rejects a not-yet-valid cert ===")
	futureCert := makeTestCert("future-node", now.Add(24*time.Hour), now.Add(48*time.Hour))
	if err := trust.Pin("future-node", futureCert); err != nil {
		fmt.Printf("  PASS: not-yet-valid cert correctly rejected: %v\n", err)
	} else {
		fmt.Println("  FAIL: a not-yet-valid certificate was accepted by Pin")
	}

	fmt.Println()
	fmt.Println("=== Test 4: Get flags an expired cert that was validly pinned earlier ===")
	// Pin a cert that's valid NOW but will have expired by the time we
	// check it — simulated here by pinning one that's already just barely
	// expired, bypassing Pin's own check by writing it a different way
	// isn't worth the complexity for a throwaway harness, so instead we
	// directly exercise Get's validity check using a cert we pin with a
	// very short remaining validity, then treat "expired" and "about to
	// expire" as the same code path being tested (Get's check is a plain
	// time comparison — there's nothing special about "just" expired vs.
	// "long" expired).
	almostExpiredButStillValid := makeTestCert("short-lived", now.Add(-time.Hour), now.Add(2*time.Second))
	if err := trust.Pin("short-lived", almostExpiredButStillValid); err != nil {
		fmt.Printf("  SKIP: could not pin the short-lived cert to test expiry-on-read: %v\n", err)
	} else {
		fmt.Println("  pinned a cert valid for 2 more seconds, waiting for it to expire...")
		time.Sleep(3 * time.Second)
		_, found, err := trust.Get("short-lived")
		if !found {
			fmt.Println("  FAIL: Get says the cert isn't found at all, expected found=true with an expiry error")
		} else if err == nil {
			fmt.Println("  FAIL: Get returned no error for a cert that has since expired")
		} else {
			fmt.Printf("  PASS: Get found the pin but correctly flagged it as expired: %v\n", err)
		}
	}

	fmt.Println()
	fmt.Println("=== Test 5: hostname validation — path traversal, backslashes, reserved names ===")
	badHostnames := []string{"../evil", "sub/dir", "..", ".", "", `foo\bar`, "CON", "con", "NUL", "com1"}
	for _, bad := range badHostnames {
		err := trust.Pin(bad, validCert)
		if err != nil {
			fmt.Printf("  REJECTED %q correctly: %v\n", bad, err)
		} else {
			fmt.Printf("  FAIL: %q was accepted but should have been rejected\n", bad)
		}
	}

	fmt.Println()
	fmt.Println("=== Test 6: Get on a hostname that was never pinned ===")
	_, found, err = trust.Get("never-paired-node")
	if err != nil {
		fmt.Printf("  FAIL: unexpected error for a never-pinned hostname: %v\n", err)
	} else if found {
		fmt.Println("  FAIL: found=true for a hostname that was never pinned")
	} else {
		fmt.Println("  PASS: correctly reports not found, no error")
	}

	fmt.Println()
	fmt.Println("=== Test 7: re-pinning the same hostname overwrites the old cert ===")
	newerCert := makeTestCert("mathesis", now.Add(-time.Minute), now.Add(48*time.Hour))
	if err := trust.Pin("mathesis", newerCert); err != nil {
		fmt.Printf("  FAIL: re-pin failed: %v\n", err)
	} else {
		peer, _, _ := trust.Get("mathesis")
		if peer.Cert.NotAfter.After(now.Add(30 * time.Hour)) {
			fmt.Println("  PASS: re-pinning replaced the old cert with the new one")
		} else {
			fmt.Println("  FAIL: Get still returns the OLD cert after re-pinning")
		}
	}
}