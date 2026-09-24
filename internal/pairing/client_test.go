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
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchAgentInfoAuthenticatesCertificateWithCode(t *testing.T) {
	const code = "ABCDEFGHJKMP"
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		certificate := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
		if err := json.NewEncoder(w).Encode(pairInfoResponse{
			AgentCertPEM: certificate,
			AgentProof:   encodeProof(code, server.Certificate().Raw),
		}); err != nil {
			t.Fatalf("encoding pair info: %v", err)
		}
	}))
	defer server.Close()

	_, err := (&Client{}).fetchAgentInfo(strings.TrimPrefix(server.URL, "https://"), code)
	if err != nil {
		t.Fatalf("fetchAgentInfo() error = %v", err)
	}
}

func TestPinnedHTTPClientRejectsSubstitutedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	otherCertificate := append([]byte(nil), server.Certificate().Raw...)
	otherCertificate[0] ^= 0xff

	client := pinnedHTTPClient(otherCertificate)
	defer client.CloseIdleConnections()
	if _, err := client.Get(server.URL); err == nil {
		t.Fatal("pinnedHTTPClient accepted a server presenting a substituted certificate")
	}
}
