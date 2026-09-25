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
	"bytes"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"time"

	"tether/internal/certs"
	"tether/internal/registry"
	"tether/internal/trust"
)

const clientTimeout = 10 * time.Second

// PairResult is what a successful Client.Pair call returns.
type PairResult struct {
	AgentHostname string
}

// Client performs the Orchestrator side of the first-contact handshake.
type Client struct {
	orchestratorIdentity *certs.Identity
	selfHostname         func() (string, error)
}

// NewClient creates a pairing Client that presents orchestratorIdentity.
func NewClient(orchestratorIdentity *certs.Identity) *Client {
	return &Client{orchestratorIdentity: orchestratorIdentity, selfHostname: registry.SelfHostname}
}

func unverifiedHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- immediately authenticated by the out-of-band proof below.
	return &http.Client{Transport: transport, Timeout: clientTimeout}
}

func pinnedHTTPClient(expectedCertDER []byte) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true, // #nosec G402 -- VerifyConnection performs exact certificate pinning.
		VerifyConnection: func(connection tls.ConnectionState) error {
			if len(connection.PeerCertificates) == 0 || subtle.ConstantTimeCompare(connection.PeerCertificates[0].Raw, expectedCertDER) != 1 {
				return fmt.Errorf("pairing endpoint did not present the code-authenticated Agent certificate")
			}
			return nil
		},
	}
	return &http.Client{Transport: transport, Timeout: clientTimeout}
}

func decodeCertificate(certPEM string, description string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil, fmt.Errorf("%s contained no valid PEM certificate", description)
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s certificate did not parse: %w", description, err)
	}
	return certificate, nil
}

func (c *Client) fetchAgentInfo(addr, code string) (*x509.Certificate, error) {
	client := unverifiedHTTPClient()
	defer client.CloseIdleConnections()

	response, err := client.Get("https://" + addr + "/pair")
	if err != nil {
		return nil, fmt.Errorf("contacting agent at %s: %w", addr, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent returned unexpected pairing-info status %d", response.StatusCode)
	}

	var info pairInfoResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, maxPairRequestBytes)).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding agent pairing information: %w", err)
	}
	agentCert, err := decodeCertificate(info.AgentCertPEM, "agent pairing information")
	if err != nil {
		return nil, err
	}
	if !validProof(code, info.AgentProof, agentCert.Raw) {
		return nil, fmt.Errorf("pairing code did not authenticate the Agent certificate")
	}
	return agentCert, nil
}

// Pair authenticates the Agent certificate with the out-of-band code, then
// sends a proof binding that Agent certificate, the Orchestrator certificate,
// and the recorded Tailnet hostname. The code itself is never transmitted.
func (c *Client) Pair(addr, code string) (*PairResult, error) {
	orchestratorHostname, err := c.selfHostname()
	if err != nil {
		return nil, fmt.Errorf("determining Orchestrator Tailnet hostname: %w", err)
	}

	agentCert, err := c.fetchAgentInfo(addr, code)
	if err != nil {
		return nil, err
	}

	orchestratorCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.orchestratorIdentity.CertDER})
	reqBody, err := json.Marshal(pairRequest{
		PairingProof:         encodeProof(code, agentCert.Raw, c.orchestratorIdentity.CertDER, []byte(orchestratorHostname)),
		OrchestratorCertPEM:  string(orchestratorCertPEM),
		OrchestratorHostname: orchestratorHostname,
	})
	if err != nil {
		return nil, fmt.Errorf("encoding pairing request: %w", err)
	}

	client := pinnedHTTPClient(agentCert.Raw)
	defer client.CloseIdleConnections()
	response, err := client.Post("https://"+addr+"/pair", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("sending authenticated pairing request to %s: %w", addr, err)
	}
	defer response.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(response.Body, maxPairRequestBytes))
	if err != nil {
		return nil, fmt.Errorf("reading pairing response from agent at %s: %w", addr, err)
	}
	if response.StatusCode != http.StatusOK {
		var errResp pairErrorResponse
		if json.Unmarshal(bodyBytes, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("agent rejected pairing (%d): %s", response.StatusCode, errResp.Error)
		}
		return nil, fmt.Errorf("agent returned unexpected status %d", response.StatusCode)
	}

	var okResp pairResponse
	if err := json.Unmarshal(bodyBytes, &okResp); err != nil {
		return nil, fmt.Errorf("decoding pairing response: %w", err)
	}
	returnedCert, err := decodeCertificate(okResp.AgentCertPEM, "pairing response")
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(returnedCert.Raw, agentCert.Raw) != 1 {
		return nil, fmt.Errorf("pairing response returned a different Agent certificate")
	}
	if agentCert.Subject.CommonName == "" {
		return nil, fmt.Errorf("agent certificate has no CommonName to identify it by")
	}
	if err := trust.Pin(agentCert.Subject.CommonName, agentCert.Raw); err != nil {
		return nil, fmt.Errorf("pairing succeeded but pinning Agent certificate failed: %w", err)
	}

	return &PairResult{AgentHostname: agentCert.Subject.CommonName}, nil
}
