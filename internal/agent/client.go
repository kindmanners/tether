package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const clientTimeout = 10 * time.Second

// StatusResult mirrors statusResponse but exported, for callers outside
// this package.
type StatusResult struct {
	Status    string
	LastError string
}

// Client is the Orchestrator-side counterpart to Server — issues
// start/stop/status commands to one specific Agent's command server,
// over a genuinely mutually-authenticated TLS connection (unlike
// pairing.Client, which deliberately skips verification for its one-time
// bootstrap exchange).
type Client struct {
	httpClient *http.Client
}

// NewClient creates a Client that will verify the Agent's certificate
// using tlsConfig — build tlsConfig with
// trust.PinnedTLSConfig(orchestratorIdentity, agentHostname, false) so
// every command is checked against the specific cert pinned during
// pairing with that Agent, not just any TLS-presenting server.
func NewClient(tlsConfig *tls.Config) *Client {
	return &Client{
		httpClient: &http.Client{
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
			Timeout:   clientTimeout,
		},
	}
}

// StartRPCServer requests addr's Agent start its ggml-rpc-server on port.
// The remote server exposes accelerator devices; the GGUF model is loaded by
// the Orchestrator-side llama-cli or llama-server, not by this process.
func (c *Client) StartRPCServer(addr string, port int) (*StatusResult, error) {
	body, err := json.Marshal(startRequest{Port: port})
	if err != nil {
		return nil, fmt.Errorf("encoding start request: %w", err)
	}
	return c.post(addr, "/start", body)
}

// StopRPCServer requests addr's Agent stop its rpc-server, if running.
func (c *Client) StopRPCServer(addr string) (*StatusResult, error) {
	return c.post(addr, "/stop", nil)
}

// GetStatus queries addr's Agent for its current rpc-server status
// without changing anything.
func (c *Client) GetStatus(addr string) (*StatusResult, error) {
	return c.GetStatusContext(context.Background(), addr)
}

// GetStatusContext lets callers enforce an operation-wide refresh budget
// instead of waiting for each individual Agent timeout in sequence.
func (c *Client) GetStatusContext(ctx context.Context, addr string) (*StatusResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+addr+"/status", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contacting agent at %s: %w", addr, err)
	}
	defer resp.Body.Close()
	return decodeStatus(resp)
}

// GetCapabilities queries the local GPU report recorded during Tether's
// machine bootstrap. It is read-only and travels over the same pinned-mTLS
// connection as status/start/stop commands.
func (c *Client) GetCapabilities(addr string) (*CapabilitiesResult, error) {
	return c.GetCapabilitiesContext(context.Background(), addr)
}

// GetCapabilitiesContext applies the same caller deadline as status probing.
func (c *Client) GetCapabilitiesContext(ctx context.Context, addr string) (*CapabilitiesResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+addr+"/capabilities", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contacting agent at %s: %w", addr, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading agent response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var errResp errorResponse
		if jsonErr := json.Unmarshal(bodyBytes, &errResp); jsonErr == nil && errResp.Error != "" {
			return nil, fmt.Errorf("agent returned %d: %s", resp.StatusCode, errResp.Error)
		}
		return nil, fmt.Errorf("agent returned unexpected status %d", resp.StatusCode)
	}

	var result CapabilitiesResult
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("decoding capability response: %w", err)
	}
	return &result, nil
}

func (c *Client) post(addr, path string, body []byte) (*StatusResult, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	resp, err := c.httpClient.Post("https://"+addr+path, "application/json", reader)
	if err != nil {
		return nil, fmt.Errorf("contacting agent at %s: %w", addr, err)
	}
	defer resp.Body.Close()
	return decodeStatus(resp)
}

func decodeStatus(resp *http.Response) (*StatusResult, error) {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading agent response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp errorResponse
		if jsonErr := json.Unmarshal(bodyBytes, &errResp); jsonErr == nil && errResp.Error != "" {
			return nil, fmt.Errorf("agent returned %d: %s", resp.StatusCode, errResp.Error)
		}
		return nil, fmt.Errorf("agent returned unexpected status %d", resp.StatusCode)
	}

	var okResp statusResponse
	if err := json.Unmarshal(bodyBytes, &okResp); err != nil {
		return nil, fmt.Errorf("decoding agent response: %w", err)
	}
	return &StatusResult{Status: okResp.Status, LastError: okResp.LastError}, nil
}
