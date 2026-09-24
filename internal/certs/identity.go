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

// Package certs handles generating and persisting the self-signed
// certificate/private-key pairs used for mTLS between the Orchestrator and
// each Agent (design doc §6.1).
//
// This package is used by BOTH cmd/tether (Orchestrator) and
// cmd/tether-agent (Agent) — both sides of an mTLS connection need their
// own keypair, not just the Agent. It lives in internal/ rather than
// inside either cmd/ directory for that reason.
//
// Deliberately NOT a CA. Each identity generates its own self-signed cert;
// there is no shared root of trust to build or manage. Trust is
// established per-pair via the pairing handshake (§6.2), which exchanges
// and pins these certs directly. See design doc §6.1 for why this was
// chosen over a local CA (option B) or Tailscale-issued certs (option C).
package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// validity is deliberately long-lived. These are pinned, self-signed certs
// with no CA and no automated renewal pipeline (a conscious choice — see
// design doc §6.1, point 4): re-pairing is the rotation mechanism, not
// scheduled renewal. A short expiry (e.g. Let's Encrypt's ~90 days) would
// force you to re-pair every node every few months for no security benefit
// in this trust model, so we deliberately go long instead.
const validity = 2 * 365 * 24 * time.Hour

// Identity holds a generated (or loaded) keypair plus its self-signed
// certificate, ready to be used in a tls.Config on either side of an mTLS
// connection.
type Identity struct {
	PrivateKey  *ecdsa.PrivateKey
	Certificate *x509.Certificate
	CertDER     []byte // raw DER bytes, needed for tls.Certificate construction
}

// Load reads an existing identity without creating or changing any local
// files. Desktop preflight uses this deliberately: opening the Agent must be
// able to explain a missing or broken identity without silently creating one
// and making the machine look partially configured.
func Load(name string) (*Identity, error) {
	if err := validateName(name); err != nil {
		return nil, fmt.Errorf("invalid identity name %q: %w", name, err)
	}
	d, err := dir()
	if err != nil {
		return nil, err
	}
	return load(filepath.Join(d, name+".key"), filepath.Join(d, name+".crt"))
}

// dir returns the directory identities are stored in:
// %AppData%\tether\certs on Windows, ~/.config/tether/certs on Linux (or
// $XDG_CONFIG_HOME/tether/certs if set), ~/Library/Application
// Support/tether/certs on macOS. os.UserConfigDir handles the
// platform-specific logic — we don't hand-roll OS detection here, since
// that's exactly the kind of thing that's easy to get subtly wrong for a
// platform you're not currently testing on.
func dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config dir: %w", err)
	}
	return filepath.Join(base, "tether", "certs"), nil
}

// LoadOrCreate loads an existing identity for the given name (e.g.
// "orchestrator" or the Agent's own hostname) from disk, or generates and
// persists a new one if none exists yet.
//
// name is used as the file prefix (name.key, name.crt) — callers on the
// Agent side should pass something stable across restarts (the machine's
// own hostname is a reasonable choice), since regenerating on every
// launch would invalidate any pairing that already happened (§6.1 step 1
// explicitly requires the private key to persist, not be regenerated).
//
// name must be a plain filename component — no path separators or "..".
// Right now every caller passes a hardcoded or hostname-derived string, so
// this validation is currently redundant in practice — but that's exactly
// why it's enforced here rather than trusted at call sites: "every current
// caller happens to be safe" is not a guarantee that holds automatically
// as new call sites appear (e.g. if a name is ever derived from
// Tailscale-reported data we don't fully control the shape of), and this
// function is the one place that can make the guarantee actually durable.
func LoadOrCreate(name string) (*Identity, error) {
	if err := validateName(name); err != nil {
		return nil, fmt.Errorf("invalid identity name %q: %w", name, err)
	}

	d, err := dir()
	if err != nil {
		return nil, err
	}

	keyPath := filepath.Join(d, name+".key")
	certPath := filepath.Join(d, name+".crt")

	if identity, err := load(keyPath, certPath); err == nil {
		return identity, nil
	} else if !os.IsNotExist(err) {
		// Distinguish "files don't exist yet" (expected on first run,
		// fall through to generation below) from any other error
		// (corrupt file, permission denied, etc.) — the latter should
		// surface to the caller rather than silently trying to overwrite
		// a file that exists but failed to parse for some other reason.
		return nil, fmt.Errorf("loading existing identity %q: %w", name, err)
	}

	identity, err := generate(name)
	if err != nil {
		return nil, fmt.Errorf("generating identity %q: %w", name, err)
	}

	if err := persist(identity, keyPath, certPath); err != nil {
		return nil, fmt.Errorf("persisting identity %q: %w", name, err)
	}

	return identity, nil
}

// Delete removes one local identity so the next LoadOrCreate call generates a
// new keypair. Re-pairing deliberately rotates both sides of the Agent's
// identity/trust relationship rather than leaving an old private key behind.
func Delete(name string) error {
	if err := validateName(name); err != nil {
		return fmt.Errorf("invalid identity name %q: %w", name, err)
	}
	d, err := dir()
	if err != nil {
		return err
	}
	for _, path := range []string{filepath.Join(d, name+".key"), filepath.Join(d, name+".crt")} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing identity %q: %w", name, err)
		}
	}
	return nil
}

// windowsReservedNames are filenames Windows treats specially regardless
// of extension — see internal/trust/store.go's copy of this same table
// for the fuller reasoning on why this is checked unconditionally on
// every platform, not just when actually running on Windows.
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// validateName rejects anything that isn't a plain filename component.
//
// Does NOT rely on filepath.Base alone for separator detection — that
// function's separator handling is platform-dependent (only '/' on
// Linux, both '/' and '\' on Windows). Since this same validation logic
// runs unmodified on both the Orchestrator (any OS) and every Agent (any
// OS), and identity names can in principle be influenced by data that
// crossed machine boundaries, a backslash needs rejecting on Linux just
// as much as on Windows — relying on the host OS's own filepath.Base to
// decide that would silently under-validate on whichever platform isn't
// currently running the check. We check both separator characters
// explicitly, then still run filepath.Base as a belt-and-suspenders
// second check for anything platform-specific it might catch beyond
// plain separators (e.g. Windows volume/prefix syntax).
//
// Also rejects "." and ".." explicitly — filepath.Base leaves both
// unchanged (nothing to strip, since neither contains a separator), so it
// alone doesn't catch them; found via testing (see internal/certs commit
// history) rather than assumed. And rejects Windows reserved device names
// (CON, NUL, COM1, etc.) unconditionally, since a name that's fine on
// Linux but breaks file creation only on Windows would otherwise surface
// as a confusing, hard-to-diagnose failure specific to one platform.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("name cannot be %q", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("name cannot contain path separators")
	}
	if filepath.Base(name) != name {
		return fmt.Errorf("name must be a plain filename component, not a path")
	}
	if windowsReservedNames[strings.ToUpper(name)] {
		return fmt.Errorf("name %q is a reserved Windows device name", name)
	}
	return nil
}

// generate creates a fresh ECDSA P-256 keypair and a self-signed
// certificate for it.
//
// ECDSA P-256 over RSA: this is a self-signed cert used only for pinned
// mTLS between machines we already control — there's no legacy client
// compatibility concern to accommodate (RSA's main advantage), and ECDSA
// gives smaller keys, faster generation, and faster handshakes. This is
// also what modern TLS deployments default to when not constrained by
// legacy requirements.
func generate(commonName string) (*Identity, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating ECDSA key: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, fmt.Errorf("generating certificate serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Ataraxia Productions - Tether"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(validity),
		// DigitalSignature only. KeyEncipherment is an RSA-key-transport
		// concept (the public key directly encrypts a symmetric key) —
		// ECDSA has no encryption operation at all, so that bit would be
		// meaningless on this cert. In TLS, an ECDSA certificate's key is
		// only ever used to sign ephemeral ECDHE parameters during the
		// handshake; the actual key agreement is ECDHE, not anything
		// this certificate's key does directly. Most TLS stacks ignore a
		// wrongly-set KeyEncipherment bit on an ECDSA cert rather than
		// erroring, but it's incorrect metadata and stricter validators
		// (some enterprise TLS inspection, HSMs) do check this strictly.
		KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth,
		},
		// Self-signed: IsCA is irrelevant here since nothing ever verifies
		// a chain up to this cert as an authority — trust comes from
		// pinning the exact cert during pairing (§6.2), not from chain
		// validation. Left false since this cert should never be used to
		// sign anything else.
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, fmt.Errorf("creating self-signed certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("parsing freshly created certificate: %w", err)
	}

	return &Identity{
		PrivateKey:  privateKey,
		Certificate: cert,
		CertDER:     certDER,
	}, nil
}

// persist writes the private key and certificate to disk as PEM files.
//
// Both files are written atomically via temp-file-then-rename, and as a
// pair: if anything fails partway through, no half-written or mismatched
// key/cert pair is left in place. This matters because os.WriteFile alone
// is not atomic — it can leave a truncated file behind if the process
// dies mid-write (crash, power loss, disk full), and a truncated key file
// on disk is worse than a missing one: LoadOrCreate sees the file exists,
// tries to parse it, fails, and errors out instead of falling back to
// generating a fresh identity — silently bricking that node until someone
// notices and manually deletes the corrupted file.
//
// The temp file is written into the SAME directory as the final
// destination, not a system temp dir — os.Rename is only atomic when
// source and destination are on the same filesystem/volume. Using a
// different directory (e.g. /tmp) risks Rename silently falling back to a
// non-atomic copy+delete on some platforms if that directory happens to
// be a different filesystem, which would defeat the entire point of this
// function.
//
// The key file is written with 0600 permissions (owner read/write only) —
// this matters more here than usual, since this private key is what
// proves this machine's identity to every paired peer; anyone else who
// can read it could impersonate this node.
func persist(identity *Identity, keyPath, certPath string) error {
	destDir := filepath.Dir(keyPath) // same dir as certPath by construction in LoadOrCreate
	if err := os.MkdirAll(destDir, 0700); err != nil {
		return fmt.Errorf("creating cert directory: %w", err)
	}

	keyBytes, err := x509.MarshalECPrivateKey(identity.PrivateKey)
	if err != nil {
		return fmt.Errorf("marshaling private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: identity.CertDER})

	// Write both temp files first. Neither has touched the real
	// destination paths yet, so if either write fails here, the existing
	// (or absent) key/cert pair at keyPath/certPath is completely
	// untouched.
	tmpKeyPath, err := writeTemp(destDir, "key-*.tmp", keyPEM, 0600)
	if err != nil {
		return fmt.Errorf("writing temporary key file: %w", err)
	}
	tmpCertPath, err := writeTemp(destDir, "crt-*.tmp", certPEM, 0644)
	if err != nil {
		os.Remove(tmpKeyPath) // best-effort cleanup of the half-finished pair
		return fmt.Errorf("writing temporary certificate file: %w", err)
	}

	// Only now do we touch the real paths, and only via Rename — which is
	// atomic on both POSIX and Windows/NTFS when source and destination
	// share a filesystem, which they do here by construction. If the key
	// rename succeeds but the cert rename somehow fails (e.g. permissions
	// change mid-operation), we're left with a NEW key but the OLD cert —
	// a mismatched pair, which is exactly the failure mode we're trying
	// to avoid. That residual risk is inherent to needing two separate
	// files for one logical identity; it's not fully eliminable without a
	// single combined file format, which would be a bigger change than
	// this fix warrants right now.
	if err := os.Rename(tmpKeyPath, keyPath); err != nil {
		os.Remove(tmpKeyPath)
		os.Remove(tmpCertPath)
		return fmt.Errorf("renaming key into place: %w", err)
	}
	if err := os.Rename(tmpCertPath, certPath); err != nil {
		return fmt.Errorf("renaming certificate into place: %w (WARNING: key file was already updated — key/cert pair may now be mismatched)", err)
	}

	return nil
}

// writeTemp writes data to a new temporary file in dir matching pattern,
// and returns its path. Using os.CreateTemp (rather than a fixed temp
// filename) avoids collisions if this ever runs concurrently, though that
// isn't expected in practice for this package.
func writeTemp(dir, pattern string, data []byte, perm os.FileMode) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	tmpPath := f.Name()

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	return tmpPath, nil
}

// load reads an existing identity back from disk. Returns an error
// satisfying os.IsNotExist if the key file doesn't exist yet — callers use
// this to distinguish "first run, need to generate" from a genuine error.
func load(keyPath, certPath string) (*Identity, error) {
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err // preserves the underlying os.IsNotExist-detectable error
	}

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("key file exists but certificate file is missing: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("no PEM block found in private key file %q", keyPath)
	}
	privateKey, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("no PEM block found in certificate file %q", certPath)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing certificate: %w", err)
	}
	if !privateKey.PublicKey.Equal(cert.PublicKey) {
		return nil, fmt.Errorf("private key does not match certificate")
	}

	return &Identity{
		PrivateKey:  privateKey,
		Certificate: cert,
		CertDER:     certBlock.Bytes,
	}, nil
}

// TLSCertificate converts this Identity into the tls.Certificate shape
// Go's crypto/tls package expects when configuring a server or client.
func (id *Identity) TLSCertificate() tls.Certificate {
	return tls.Certificate{
		Certificate: [][]byte{id.CertDER},
		PrivateKey:  id.PrivateKey,
		Leaf:        id.Certificate,
	}
}
