// Command certs-test is a throwaway harness to verify internal/certs
// before it's wired into the real Agent/Orchestrator pairing flow. Delete
// once the pairing handshake (§6.2) exists and exercises this code for
// real.
package main

import (
	"encoding/hex"
	"fmt"
	"os"

	"tether/internal/certs"
)

func fingerprint(certDER []byte) string {
	// Not a cryptographic fingerprint (no hashing) — just enough to
	// eyeball "is this the same cert across two calls" without dumping
	// the entire DER blob. Good enough for this throwaway check; the real
	// pairing handshake will need a proper hash-based fingerprint later.
	if len(certDER) > 8 {
		return hex.EncodeToString(certDER[:8])
	}
	return hex.EncodeToString(certDER)
}

func main() {
	fmt.Println("=== Test 1: generate + persist on first call ===")
	id1, err := certs.LoadOrCreate("test-identity")
	if err != nil {
		fmt.Fprintf(os.Stderr, "first LoadOrCreate failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  generated. cert fingerprint (first 8 bytes): %s\n", fingerprint(id1.CertDER))
	fmt.Printf("  cert subject: %s\n", id1.Certificate.Subject.CommonName)
	fmt.Printf("  valid from %s to %s\n", id1.Certificate.NotBefore.Format("2006-01-02"), id1.Certificate.NotAfter.Format("2006-01-02"))

	fmt.Println()
	fmt.Println("=== Test 2: reload on second call should return the SAME identity ===")
	id2, err := certs.LoadOrCreate("test-identity")
	if err != nil {
		fmt.Fprintf(os.Stderr, "second LoadOrCreate failed: %v\n", err)
		os.Exit(1)
	}
	fp1, fp2 := fingerprint(id1.CertDER), fingerprint(id2.CertDER)
	fmt.Printf("  first call fingerprint:  %s\n", fp1)
	fmt.Printf("  second call fingerprint: %s\n", fp2)
	if fp1 == fp2 {
		fmt.Println("  PASS: identity persisted correctly across calls")
	} else {
		fmt.Println("  FAIL: got a different identity on reload — persistence is broken")
	}

	fmt.Println()
	fmt.Println("=== Test 3: name validation should reject path traversal ===")
	badNames := []string{"../evil", "sub/dir", "..", ".", ""}
	for _, bad := range badNames {
		_, err := certs.LoadOrCreate(bad)
		if err != nil {
			fmt.Printf("  REJECTED %q correctly: %v\n", bad, err)
		} else {
			fmt.Printf("  FAIL: %q was accepted but should have been rejected\n", bad)
		}
	}

	fmt.Println()
	fmt.Println("=== Test 4: TLSCertificate() conversion doesn't panic ===")
	tlsCert := id1.TLSCertificate()
	fmt.Printf("  PASS: got tls.Certificate with %d cert bytes, leaf CN=%s\n",
		len(tlsCert.Certificate[0]), tlsCert.Leaf.Subject.CommonName)

	fmt.Println()
	fmt.Println("Done. Check the platform-specific config dir manually to confirm file locations:")
	fmt.Println("  Windows: %AppData%\\tether\\certs\\")
	fmt.Println("  Linux:   ~/.config/tether/certs/")
}
