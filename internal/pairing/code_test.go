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

package pairing

import (
	"strings"
	"testing"
	"time"
)

func TestGeneratedCodeUsesSixtyBitsOfAlphabetEntropy(t *testing.T) {
	code, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(code.String()) != codeLength {
		t.Fatalf("code length = %d, want %d", len(code.String()), codeLength)
	}
	for _, character := range code.String() {
		if !strings.ContainsRune(codeAlphabet, character) {
			t.Fatalf("code contains character %q outside the pairing alphabet", character)
		}
	}
}

func TestConsumeProofBindsBothCertificatesAndHostname(t *testing.T) {
	code := &Code{value: "ABCDEFGHJKMP", expiresAt: time.Now().Add(time.Minute)}
	agentCert := []byte("agent certificate")
	orchestratorCert := []byte("orchestrator certificate")
	hostname := []byte("orchestrator.tailnet")

	valid := encodeProof(code.value, agentCert, orchestratorCert, hostname)
	if !code.ConsumeProof(valid, agentCert, orchestratorCert, hostname) {
		t.Fatal("ConsumeProof rejected a valid certificate-bound proof")
	}
	if code.ConsumeProof(valid, agentCert, orchestratorCert, hostname) {
		t.Fatal("ConsumeProof accepted a replay after consuming the code")
	}
}

func TestConsumeProofRejectsChangedBindingWithoutConsuming(t *testing.T) {
	code := &Code{value: "ABCDEFGHJKMP", expiresAt: time.Now().Add(time.Minute)}
	agentCert := []byte("agent certificate")
	orchestratorCert := []byte("orchestrator certificate")
	hostname := []byte("orchestrator.tailnet")
	valid := encodeProof(code.value, agentCert, orchestratorCert, hostname)

	if code.ConsumeProof(valid, agentCert, []byte("attacker certificate"), hostname) {
		t.Fatal("ConsumeProof accepted a proof with a substituted Orchestrator certificate")
	}
	if !code.ConsumeProof(valid, agentCert, orchestratorCert, hostname) {
		t.Fatal("an invalid proof consumed the code and blocked the valid pairing")
	}
}
