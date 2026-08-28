package pairing

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"time"

	"tether/internal/certs"
	"tether/internal/trust"
)

// clientTimeout bounds a single pairing HTTP request. Deliberately short —
// this is a local-network (Tailscale) call to a machine that's supposed to
// be actively listening for exactly this request right now; there's no
// legitimate reason for it to hang.
const clientTimeout = 10 * time.Second

// pairingHTTPClient is used ONLY for the bootstrap /pair exchange. It sets
// InsecureSkipVerify because, at this point in the flow, the Orchestrator
// has no pinned certificate for the Agent it's contacting — that's the
// entire problem pairing solves, so there's nothing yet to verify the
// Agent's TLS certificate against. This is safe specifically because the
// pairing Code (supplied out-of-band by a human) is the real
// authentication mechanism here, not the TLS certificate — TLS at this
// stage buys confidentiality-in-transit against passive observation, not
// identity verification.
//
// This client (or InsecureSkipVerify generally) must NEVER be reused for
// any connection after a successful pairing. Every connection after
// pairing must verify against the specific certificate trust.Pin stored
// during this exchange — using a client that skips verification there
// would silently throw away the entire point of pairing.
var pairingHTTPClient = &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
	Timeout: clientTimeout,
}

// PairResult is what a successful Client.Pair call returns.
type PairResult struct {
	// AgentHostname is the hostname the Agent's certificate identifies
	// itself as (its CommonName), which is also the key it was pinned
	// under in internal/trust.
	AgentHostname string
}

// Client performs the Orchestrator side of the pairing handshake (design
// doc §6.2) against one specific Agent. Construct with the Orchestrator's
// own identity (presented to the Agent as part of the exchange), then
// call Pair once per pairing attempt.
type Client struct {
	orchestratorIdentity *certs.Identity
}

// NewClient creates a pairing Client that will present orchestratorIdentity
// as this Orchestrator's own certificate during any pairing it performs.
func NewClient(orchestratorIdentity *certs.Identity) *Client {
	return &Client{orchestratorIdentity: orchestratorIdentity}
}

// Pair attempts to pair with an Agent at addr (e.g. "100.114.155.22:7420")
// using code — the pairing code a human read off the Agent (or the
// Orchestrator, depending on which side displays it) and typed in here.
//
// On success, the Agent's certificate is parsed AND PINNED via
// internal/trust before Pair returns — the caller does not need to (and
// should not need to) do this separately. This mirrors what
// Server.handlePair already does for the Orchestrator's cert on the
// Agent side: keeping "pin on successful pairing" as pairing's own
// responsibility, rather than something every caller has to remember to
// do afterward, since there's no legitimate reason to pair without
// pinning the result.
//
// A wrong code, a network error, or a malformed response all return a
// non-nil error and pin nothing. Per Server's design, a wrong code does
// NOT necessarily mean the pairing session is over — the Agent's Server
// keeps listening for further attempts within its window (see
// Code.Consume's doc comment for why), so a caller may reasonably retry
// Pair with a corrected code against the same addr, as long as the
// Agent's window hasn't closed yet.
func (c *Client) Pair(addr, code string) (*PairResult, error) {
	orchestratorCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: c.orchestratorIdentity.CertDER,
	})

	reqBody, err := json.Marshal(pairRequest{
		PairingCode:         code,
		OrchestratorCertPEM: string(orchestratorCertPEM),
	})
	if err != nil {
		return nil, fmt.Errorf("encoding pairing request: %w", err)
	}

	resp, err := pairingHTTPClient.Post(
		"https://"+addr+"/pair",
		"application/json",
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return nil, fmt.Errorf("contacting agent at %s: %w", addr, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response from agent at %s: %w", addr, err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp pairErrorResponse
		if jsonErr := json.Unmarshal(bodyBytes, &errResp); jsonErr == nil && errResp.Error != "" {
			return nil, fmt.Errorf("agent rejected pairing (%d): %s", resp.StatusCode, errResp.Error)
		}
		return nil, fmt.Errorf("agent returned unexpected status %d", resp.StatusCode)
	}

	var okResp pairResponse
	if err := json.Unmarshal(bodyBytes, &okResp); err != nil {
		return nil, fmt.Errorf("decoding agent's response: %w", err)
	}

	block, _ := pem.Decode([]byte(okResp.AgentCertPEM))
	if block == nil {
		return nil, fmt.Errorf("agent's response contained no valid PEM certificate")
	}
	agentCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("agent's certificate did not parse: %w", err)
	}

	agentHostname := agentCert.Subject.CommonName
	if agentHostname == "" {
		return nil, fmt.Errorf("agent's certificate has no CommonName to identify it by")
	}

	if err := trust.Pin(agentHostname, block.Bytes); err != nil {
		return nil, fmt.Errorf("pairing succeeded but pinning agent certificate failed: %w", err)
	}

	return &PairResult{AgentHostname: agentHostname}, nil
}