// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package acceptance

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrchestratorVerifierWritesSanitizedEvidence(t *testing.T) {
	const apiKey = "acceptance-test-secret"
	rpcEndpoint := closedEndpoint(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"object":"list","data":[{"id":"test-model","object":"model","owned_by":"tether"}]}`)
		case "/api/v1/models/test-model/plan":
			fmt.Fprint(w, `{"model":"test-model","sizeBytes":4,"reserveBytes":8,"modelReserveBytes":6,"kvCacheBytes":2,"mode":"split","nodes":["private-node-a","private-node-b"],"observedAt":"2026-09-30T00:00:00Z"}`)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Tether acceptance stream received.\"}}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		case "/api/v1/model-states":
			fmt.Fprint(w, `{"models":[{"model":"test-model","state":"loaded","nodes":["private-node-a","private-node-b"],"updatedAt":"2026-09-30T00:00:01Z"}]}`)
		case "/api/v1/models/test-model/unload":
			fmt.Fprint(w, `{"models":[{"model":"test-model","state":"unloaded","updatedAt":"2026-09-30T00:00:02Z"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	resultDir := t.TempDir()
	script, err := filepath.Abs("orchestrator.ps1")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		`& $env:TETHER_TEST_SCRIPT -BaseUrl $env:TETHER_TEST_URL -ApiKey $env:TETHER_TEST_KEY -Model 'test-model' -RequireDistributed -RunInference -UnloadAfter -RpcEndpoint @($env:TETHER_TEST_RPC_A,$env:TETHER_TEST_RPC_B) -OutputDirectory $env:TETHER_TEST_OUTPUT`)
	command.Env = append(os.Environ(),
		"TETHER_TEST_SCRIPT="+script,
		"TETHER_TEST_URL="+server.URL,
		"TETHER_TEST_KEY="+apiKey,
		"TETHER_TEST_RPC_A="+rpcEndpoint,
		"TETHER_TEST_RPC_B="+rpcEndpoint,
		"TETHER_TEST_OUTPUT="+resultDir,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("orchestrator verifier failed: %v\n%s", err, output)
	}

	summaryBytes, err := os.ReadFile(filepath.Join(resultDir, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var summary struct {
		Passed             bool   `json:"passed"`
		PlacementMode      string `json:"placementMode"`
		PlacementNodeCount int    `json:"placementNodeCount"`
	}
	if err := json.Unmarshal(summaryBytes, &summary); err != nil {
		t.Fatal(err)
	}
	if !summary.Passed || summary.PlacementMode != "split" || summary.PlacementNodeCount != 2 {
		t.Fatalf("unexpected acceptance summary: %#v", summary)
	}

	for _, name := range []string{"summary.json", "environment.json", "models.json", "placement.json", "rpc-exposure.json", "model-state.json", "completion.sse"} {
		contents, err := os.ReadFile(filepath.Join(resultDir, name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(contents)
		if strings.Contains(text, apiKey) || strings.Contains(text, "private-node-a") || strings.Contains(text, "private-node-b") {
			t.Fatalf("%s contains secret or private node name: %s", name, text)
		}
	}
}

func closedEndpoint(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return endpoint
}
