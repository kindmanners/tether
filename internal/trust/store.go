// Package trust manages the set of peer certificates this machine has
// pinned via a successful pairing (design doc §6.1, §6.2). This is
// deliberately separate from internal/certs: certs handles THIS machine's
// own identity (its own keypair), while trust handles which OTHER
// machines' certs this one has chosen to believe. Conflating the two
// would mean a single package holding both "who am I" and "who do I
// trust" — different concerns with different lifecycles (your own
// identity persists for the life of the install; trust entries are added
// one at a time, per pairing, and could in principle be revoked
// individually later).
//
// There is no CA and no chain validation here, matching §6.1's decision:
// trust comes from having the exact peer certificate on file (pinning),
// not from verifying a signature chain up to some shared root.
package trust

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// dir returns the directory trust records are stored in — a sibling of
// internal/certs' own storage location (same os.UserConfigDir() base),
// but its own subfolder, keeping "my identity" and "who I trust"
// physically separate on disk as well as in code.
func dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config dir: %w", err)
	}
	return filepath.Join(base, "tether", "trust"), nil
}

// PeerCert is one pinned peer certificate, with the metadata worth keeping
// alongside it.
type PeerCert struct {
	Hostname  string
	Cert      *x509.Certificate
	TrustedAt time.Time
}

// Pin records a peer's certificate as trusted, keyed by hostname, and
// persists it to disk immediately. This is the operation a successful
// pairing handshake calls once the incoming cert has been validated
// against the pairing code (§6.2) — Pin itself does not re-validate the
// pairing code or handshake; by the time this is called, the caller has
// already decided this cert should be trusted. Pin DOES check the cert's
// own validity window (NotBefore/NotAfter) — pinning is about identity
// (exact-match, no chain), not about temporal validity, but there's no
// reason to accept and persist a cert that's already expired or not yet
// valid, since crypto/tls will reject it at actual handshake time
// regardless — better to fail clearly here, at the point where the
// problem is obvious, than defer it to a confusing TLS handshake error
// much later.
//
// Pinning the same hostname again OVERWRITES the previous entry. This
// matters for re-pairing (§6.1 step 4's rotation mechanism) — if a node's
// cert needs to change, re-running pairing should simply replace the old
// pinned cert, not require a separate "unpin first" step.
func Pin(hostname string, certDER []byte) error {
	if err := validateHostname(hostname); err != nil {
		return fmt.Errorf("invalid hostname %q: %w", hostname, err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return fmt.Errorf("parsing certificate for %q: %w", hostname, err)
	}
	if err := checkValidityWindow(cert); err != nil {
		return fmt.Errorf("certificate for %q: %w", hostname, err)
	}

	d, err := dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0700); err != nil {
		return fmt.Errorf("creating trust directory: %w", err)
	}

	path := filepath.Join(d, hostname+".crt")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	// Same atomic-write pattern as internal/certs (temp file in the same
	// directory, then Rename) — a torn write here would leave a corrupted
	// trust record for a peer, which on next load would either fail to
	// parse (locking that peer out until manually fixed) or, worse if the
	// corruption were ever subtly wrong rather than obviously broken,
	// pin something other than what was actually agreed during pairing.
	tmp, err := os.CreateTemp(d, "pin-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(certPEM); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming into place: %w", err)
	}

	return nil
}

// checkValidityWindow reports an error if cert is not currently within
// its NotBefore/NotAfter window. Shared by both Pin (reject expired certs
// up front) and Get (flag an already-pinned cert that has since expired —
// e.g. pinned a year ago with a validity period shorter than expected).
func checkValidityWindow(cert *x509.Certificate) error {
	now := time.Now()
	if now.Before(cert.NotBefore) {
		return fmt.Errorf("certificate is not yet valid (NotBefore: %s)", cert.NotBefore.Format(time.RFC3339))
	}
	if now.After(cert.NotAfter) {
		return fmt.Errorf("certificate has expired (NotAfter: %s)", cert.NotAfter.Format(time.RFC3339))
	}
	return nil
}

// Get returns the pinned certificate for hostname, if one exists.
//
// If a pinned certificate exists but has since expired (or, unusually,
// isn't valid yet), Get still returns it — the caller may have a
// legitimate reason to inspect an expired pin (e.g. showing "this node's
// cert expired, re-pair it" in a UI) — but the returned error is non-nil
// and describes the validity problem, so a caller that just wants a
// currently-usable cert can check err before trusting the result. This
// mirrors Pin's up-front validity check without silently hiding an
// expired-but-present pin from callers that want to know it exists.
func Get(hostname string) (*PeerCert, bool, error) {
	if err := validateHostname(hostname); err != nil {
		return nil, false, fmt.Errorf("invalid hostname %q: %w", hostname, err)
	}

	d, err := dir()
	if err != nil {
		return nil, false, err
	}

	path := filepath.Join(d, hostname+".crt")
	info, statErr := os.Stat(path)
	if os.IsNotExist(statErr) {
		return nil, false, nil
	} else if statErr != nil {
		return nil, false, fmt.Errorf("checking trust record for %q: %w", hostname, statErr)
	}

	certPEM, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("reading trust record for %q: %w", hostname, err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, false, fmt.Errorf("no PEM block found in trust record for %q", hostname)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, false, fmt.Errorf("parsing trust record for %q: %w", hostname, err)
	}

	peerCert := &PeerCert{
		Hostname:  hostname,
		Cert:      cert,
		TrustedAt: info.ModTime(),
	}

	if err := checkValidityWindow(cert); err != nil {
		return peerCert, true, fmt.Errorf("pinned certificate for %q: %w", hostname, err)
	}

	return peerCert, true, nil
}

// windowsReservedNames are filenames Windows treats specially regardless
// of extension (CON, CON.txt, con.anything all collide with the reserved
// device name). Creating a file with one of these names fails on Windows
// even though the same name is completely unremarkable on Linux — a real
// hazard for hostnames, since Tailscale hostnames are user-chosen and
// nothing stops someone naming a machine "con" or "aux". Checked
// case-insensitively since Windows filenames are case-insensitive.
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// validateHostname applies the same plain-filename-component restriction
// as internal/certs' validateName, for the identical underlying reason:
// hostname becomes part of a file path (hostname+".crt"), so it must not
// be usable to escape the trust directory or collide with a reserved
// filesystem name.
//
// This does NOT reuse filepath.Base for the separator check, unlike
// internal/certs' original approach — filepath.Base's separator handling
// is platform-dependent (it only recognizes '/' on Linux, both '/' and
// '\' on Windows), which matters a lot here specifically because this is
// a networked, cross-platform system: a hostname could be generated on
// one OS and validated (or written to a path) on another. A backslash in
// a hostname is inert to filepath.Base on Linux, but would need rejecting
// there just as much as on Windows, since this same validation logic runs
// unmodified on both. We check both separator characters explicitly and
// unconditionally instead of trusting the platform-specific stdlib
// behavior to do it for us. Also checks Windows reserved device names
// (CON, NUL, COM1, etc.) — these fail file creation only on Windows, but
// since this code path can run on either OS, and the failure mode
// (pairing silently breaks for a specific hostname) would be confusing
// and hard to diagnose from a Linux developer's perspective, we reject
// them unconditionally rather than only when actually running on
// Windows.
func validateHostname(hostname string) error {
	if hostname == "" {
		return fmt.Errorf("hostname cannot be empty")
	}
	if hostname == "." || hostname == ".." {
		return fmt.Errorf("hostname cannot be %q", hostname)
	}
	if strings.ContainsAny(hostname, `/\`) {
		return fmt.Errorf("hostname cannot contain path separators")
	}
	// Belt-and-suspenders: filepath.Base still catches anything the
	// explicit separator check might miss on the current platform (e.g.
	// platform-specific volume/prefix handling on Windows), even though
	// it's no longer the primary defense.
	if filepath.Base(hostname) != hostname {
		return fmt.Errorf("hostname must be a plain filename component, not a path")
	}

	// Compare against the name without any extension, since Windows
	// treats "CON.txt" as reserved too, not just a bare "CON" — but our
	// filenames are always hostname+".crt", so stripping our own known
	// suffix pattern isn't even necessary here; the hostname itself,
	// unmodified, is what we compare.
	if windowsReservedNames[strings.ToUpper(hostname)] {
		return fmt.Errorf("hostname %q is a reserved Windows device name", hostname)
	}

	return nil
}