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

package trust

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"tether/internal/certs"
	"time"
)

func TestPinGetRepinAndHostnameValidation(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("APPDATA", configHome)
	now := time.Now()
	first := testCertificate(t, "node", now.Add(-time.Hour), now.Add(time.Hour))
	if err := Pin("node", first); err != nil {
		t.Fatal(err)
	}
	peer, found, err := Get("node")
	if err != nil || !found || peer.Hostname != "node" || !bytes.Equal(peer.Cert.Raw, first) || peer.TrustedAt.IsZero() {
		t.Fatalf("Get(node) = %#v, found %t, err %v", peer, found, err)
	}
	second := testCertificate(t, "node", now.Add(-time.Hour), now.Add(2*time.Hour))
	if err := Pin("node", second); err != nil {
		t.Fatal(err)
	}
	peer, found, err = Get("node")
	if err != nil || !found || !bytes.Equal(peer.Cert.Raw, second) {
		t.Fatalf("Get(node) after re-pin = %#v, found %t, err %v", peer, found, err)
	}
	if _, found, err := Get("missing"); err != nil || found {
		t.Fatalf("Get(missing) = found %t, err %v", found, err)
	}
	for _, hostname := range []string{"", ".", "..", "../evil", "sub/dir", `sub\dir`, "CON", "nul", "COM1"} {
		if err := Pin(hostname, first); err == nil {
			t.Errorf("Pin(%q) succeeded", hostname)
		}
	}
}

func TestPinAndGetEnforceCertificateValidity(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("APPDATA", configHome)
	now := time.Now()
	expired := testCertificate(t, "expired", now.Add(-2*time.Hour), now.Add(-time.Hour))
	future := testCertificate(t, "future", now.Add(time.Hour), now.Add(2*time.Hour))
	if err := Pin("expired", expired); err == nil {
		t.Fatal("Pin accepted an expired certificate")
	}
	if err := Pin("future", future); err == nil {
		t.Fatal("Pin accepted a not-yet-valid certificate")
	}

	trustDir, err := dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(trustDir, 0o700); err != nil {
		t.Fatal(err)
	}
	contents := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: expired})
	if err := os.WriteFile(filepath.Join(trustDir, "expired.crt"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	peer, found, err := Get("expired")
	if err == nil || !found || peer == nil {
		t.Fatalf("Get(expired) = %#v, found %t, err %v", peer, found, err)
	}
}

func testCertificate(t *testing.T, commonName string, notBefore, notAfter time.Time) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
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
		t.Fatal(err)
	}
	return der
}

func TestDeleteRemovesPinnedPeer(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("APPDATA", configHome)
	identity, err := certs.LoadOrCreate("agent")
	if err != nil {
		t.Fatal(err)
	}
	if err := Pin("orchestrator", identity.CertDER); err != nil {
		t.Fatal(err)
	}
	if err := Delete("orchestrator"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Get("orchestrator"); err != nil || found {
		t.Fatalf("Get after Delete = found %t, err %v", found, err)
	}
}
