package scripts

import (
	"regexp"
	"testing"
)

func TestLlamaCppRevisionIsSingleExactSHA(t *testing.T) {
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(LlamaCppRevision()) {
		t.Fatalf("LlamaCppRevision() = %q, want lowercase 40-character SHA", LlamaCppRevision())
	}
	sha := regexp.MustCompile(`[0-9a-f]{40}`)
	for name, script := range map[string][]byte{
		"windows node": WindowsNodeBootstrap, "linux node": LinuxNodeBootstrap,
		"windows orchestrator": WindowsOrchestratorBootstrap, "linux orchestrator": LinuxOrchestratorBootstrap,
	} {
		if sha.Match(script) {
			t.Fatalf("%s bootstrap contains a hard-coded SHA instead of using the reviewed argument", name)
		}
	}
}
