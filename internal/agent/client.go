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

package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	tlsConfig  *tls.Config
}

// NewClient creates a Client that will verify the Agent's certificate
// using tlsConfig — build tlsConfig with
// trust.PinnedTLSConfig(orchestratorIdentity, agentHostname, false) so
// every command is checked against the specific cert pinned during
// pairing with that Agent, not just any TLS-presenting server.
func NewClient(tlsConfig *tls.Config) *Client {
	clientTLS := tlsConfig.Clone()
	clientTLS.NextProtos = []string{"http/1.1"}
	return &Client{
		httpClient: &http.Client{
			Transport: &http.Transport{TLSClientConfig: clientTLS},
			Timeout:   clientTimeout,
		},
		tlsConfig: clientTLS,
	}
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// DialRPC opens one raw llama.cpp RPC stream through the Agent's HTTP/1.1
// CONNECT endpoint. Setup is bounded, but successful streams have no deadline.
func (c *Client) DialRPC(ctx context.Context, addr string) (net.Conn, error) {
	if c.tlsConfig == nil {
		return nil, fmt.Errorf("RPC dialing requires a pinned TLS configuration")
	}
	setupCtx, cancel := context.WithTimeout(ctx, rpcConnectTimeout)
	defer cancel()
	tlsConfig := c.tlsConfig.Clone()
	tlsConfig.NextProtos = []string{"http/1.1"}
	conn, err := (&tls.Dialer{Config: tlsConfig}).DialContext(setupCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connecting to Agent %s: %w", addr, err)
	}
	fail := func(err error) (net.Conn, error) {
		_ = conn.Close()
		return nil, err
	}
	deadline := time.Now().Add(rpcConnectTimeout)
	if actual, ok := setupCtx.Deadline(); ok && actual.Before(deadline) {
		deadline = actual
	}
	_ = conn.SetDeadline(deadline)
	stopCancel := context.AfterFunc(setupCtx, func() { _ = conn.SetDeadline(time.Now()) })
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Path: "/rpc"}, Host: "tether-agent"}
	if err := request.Write(conn); err != nil {
		stopCancel()
		return fail(fmt.Errorf("sending RPC CONNECT request: %w", err))
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	stopCancel()
	if err != nil {
		return fail(fmt.Errorf("reading RPC CONNECT response: %w", err))
	}
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		return fail(fmt.Errorf("agent rejected RPC CONNECT with %s: %s", response.Status, bytes.TrimSpace(message)))
	}
	if err := setupCtx.Err(); err != nil {
		return fail(fmt.Errorf("establishing RPC CONNECT: %w", err))
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fail(fmt.Errorf("clearing RPC stream deadline: %w", err))
	}
	return &bufferedConn{Conn: conn, reader: reader}, nil
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
