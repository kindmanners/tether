package trust

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
)

// PinnedTLSConfig builds a *tls.Config that verifies a peer's certificate
// against a specific pinned entry from this trust store, NOT against a
// certificate authority chain. This is the ongoing command-channel trust
// model (design doc §3) that pairing (§6.2) exists to establish — unlike
// pairing's own bootstrap TLS (which skips verification entirely, since
// nothing is pinned yet), every connection AFTER a successful pairing
// should use this.
//
// selfIdentity is presented as this side's own certificate. expectedPeer
// is the hostname whose pinned cert (via Get) the OTHER side's
// certificate must exactly match — not chain-validate against, MATCH,
// byte for byte. There is no CA here (design doc §6.1): a self-signed
// cert dropped into tls.Config.RootCAs/ClientCAs would happen to work for
// chain validation purposes, since a self-signed cert is trivially its
// own root — but relying on that coincidence would conflate "chain
// validates" with the actual question we care about, "is this exactly
// the specific certificate I pinned for this specific peer." This uses
// InsecureSkipVerify plus a custom VerifyPeerCertificate callback instead,
// which per crypto/tls's own documentation is how normal verification
// gets replaced by custom logic — an explicit choice, not a shortcut.
// (On the server side this also requires ClientAuth: RequireAnyClientCert
// specifically, not RequireAndVerifyClientCert — see the comment where
// ClientAuth is set below for why that distinction is load-bearing, not
// stylistic.)
//
// requireClientCert controls whether this config is used as a TLS
// SERVER that demands a client certificate (tls.RequireAndVerifyClientCert)
// or as a TLS CLIENT presenting its own certificate to a server. Both
// sides of a real mTLS connection need this same exact-pin verification
// logic — just wired to the appropriate side of Go's tls.Config for
// their role — which is why this is one shared function rather than
// separate near-duplicate implementations for client and server.
func PinnedTLSConfig(selfIdentity tls.Certificate, expectedPeerHostname string, requireClientCert bool) (*tls.Config, error) {
	expectedPeer, found, err := Get(expectedPeerHostname)
	if err != nil {
		return nil, fmt.Errorf("looking up pinned cert for %q: %w", expectedPeerHostname, err)
	}
	if !found {
		return nil, fmt.Errorf("no pinned certificate for %q — has pairing been completed with this peer?", expectedPeerHostname)
	}

	verify := func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("peer presented no certificate")
		}
		// Only the leaf (first) certificate matters here — there is no
		// chain to walk, no intermediates to consider. Exact match on
		// the raw bytes against what Pin stored during pairing.
		presented := rawCerts[0]
		if !bytesEqual(presented, expectedPeer.Cert.Raw) {
			return fmt.Errorf("peer certificate does not match the pinned certificate for %q — possible identity mismatch or a stale/rotated cert that needs re-pairing", expectedPeerHostname)
		}
		return nil
	}

	cfg := &tls.Config{
		Certificates:          []tls.Certificate{selfIdentity},
		InsecureSkipVerify:    true, // see doc comment above — verification is done in VerifyPeerCertificate instead
		VerifyPeerCertificate: verify,
	}

	if requireClientCert {
		// RequireAnyClientCert, NOT RequireAndVerifyClientCert. This
		// matters and was verified against Go's own documentation
		// rather than assumed: per crypto/tls's docs, VerifyPeerCertificate
		// only replaces normal verification entirely — skipping Go's own
		// chain-based check — when ClientAuth is RequestClientCert or
		// RequireAnyClientCert. RequireAndVerifyClientCert still runs
		// real chain verification against ClientCAs regardless of
		// InsecureSkipVerify or a custom VerifyPeerCertificate — and
		// since we never populate ClientCAs (there is no CA in this
		// design, see design doc §6.1), that would fail every
		// connection, legitimate or not, with an empty trust pool. An
		// earlier version of this file used RequireAndVerifyClientCert
		// and a comment claiming InsecureSkipVerify would bypass it —
		// that claim was checked against Go's actual documentation and
		// found to be wrong before this code was ever run, not
		// discovered by observing a failure.
		cfg.ClientAuth = tls.RequireAnyClientCert
	}

	return cfg, nil
}

// bytesEqual is a tiny local helper rather than pulling in bytes.Equal
// just for this one call site — kept here so this file's dependencies
// stay minimal and obviously complete at a glance.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}