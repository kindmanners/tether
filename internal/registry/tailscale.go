package registry

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"tether/internal/executil"
)

// tailscalePeer mirrors the subset of fields Tailscale's `tailscale status
// --json` output provides per peer, as documented in Tailscale's own
// ipn/ipnstate.PeerStatus struct (pkg.go.dev/tailscale.com/ipn/ipnstate).
//
// Deliberately only the fields we actually use are declared here — this is
// not a full mirror of PeerStatus. json.Unmarshal ignores JSON fields with
// no matching Go struct field, so this stays forward-compatible with new
// fields Tailscale adds later without needing an update.
//
// Important: HostName is documented by Tailscale itself as "HostInfo's
// Hostname (not a DNS name and not necessarily unique)". DNSName is the
// peer's actual FQDN (form: "host.<MagicDNSSuffix>.", trailing dot
// included) and is what we match against the allowlist's hostnames, since
// it's the field Tailscale treats as the stable identifier.
type tailscalePeer struct {
	HostName     string   `json:"HostName"`
	DNSName      string   `json:"DNSName"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	Online       bool     `json:"Online"`
}

// tailscaleStatus mirrors the top-level shape of `tailscale status --json`.
// Peer is a map keyed by an opaque node key string (e.g. "nodekey:abc..."),
// NOT an array — getting this wrong (using a slice) would compile fine but
// silently unmarshal zero peers, since json.Unmarshal doesn't error on a
// JSON object being decoded into a slice-shaped mismatch at this nesting
// level; it just fails to populate it. Worth knowing if this ever needs
// debugging.
//
// Self is the machine tailscale itself is running on — it is NOT included
// in Peer (Tailscale never lists a node as its own peer). Without reading
// Self separately, the Orchestrator would be structurally blind to itself
// whenever it runs on the same machine as an inference node, which is a
// real, expected setup (e.g. someone with two gaming PCs and no separate
// server machine). See QueryTailscalePeers for how Self gets merged in.
type tailscaleStatus struct {
	Peer map[string]tailscalePeer `json:"Peer"`
	Self tailscalePeer            `json:"Self"`
}

// LivePeer is the parsed, cleaned-up result of one Tailscale peer, ready to
// be matched against the allowlist. This is intentionally not the same type
// as tailscalePeer: LivePeer.Hostname is the short-form name with the
// MagicDNS suffix and trailing dot stripped (e.g. "ataraxia", not
// "ataraxia.tailnet-name.ts.net."), because that's the form your
// node_allowlist.yaml hostnames are written in, and IPv4Address is picked
// out from the TailscaleIPs list rather than left as a raw slice, since
// registry.go only ever wants one address per node.
type LivePeer struct {
	Hostname    string
	IPv4Address string
	Online      bool
	SeenAt      time.Time
}

// SelfHostname returns just this machine's own Tailscale short hostname
// (e.g. "mathesis"), without the peer list. This is what the Agent uses
// as its own identity name for internal/certs and internal/trust — using
// the SAME hostname source the registry already uses (Tailscale's
// DNSName, not the OS's own os.Hostname()) matters here specifically:
// os.Hostname() can diverge from the Tailscale-derived name (e.g.
// Eudaimonia's OS hostname is "Sand-PC", but its Tailscale DNSName-derived
// short hostname is "eudaimonia" — the same HostName-vs-DNSName mismatch
// documented on tailscalePeer above). If the Agent identified itself
// using a different naming source than the registry uses to identify the
// same machine, the two could silently disagree about what to call the
// same node.
//
// Internally this calls the same `tailscale status --json` path as
// QueryTailscalePeers rather than a separate lighter-weight query —
// there's no cheaper way to get Self's DNSName than asking Tailscale for
// full status, so we don't avoid the shell-out, but we do avoid building
// the full peer list unnecessarily by not calling QueryTailscalePeers
// itself.
func SelfHostname() (string, error) {
	cmd := exec.Command("tailscale", "status", "--json")
	executil.HideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("tailscale status exited with error: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("running 'tailscale status --json': %w (is tailscale installed and on PATH?)", err)
	}

	var status tailscaleStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return "", fmt.Errorf("parsing tailscale status JSON: %w", err)
	}

	hostname := shortHostname(status.Self.DNSName)
	if hostname == "" {
		return "", fmt.Errorf("tailscale status returned no usable DNSName for Self — is tailscale running and this device registered?")
	}
	return hostname, nil
}

// QueryTailscalePeers shells out to `tailscale status --json`, parses the
// output, and returns a cleaned-up list of live peers.
//
// This function does not know about the allowlist and does not decide
// which peers matter to Tether — it only answers "what does Tailscale
// currently say is on this tailnet." The merge against the allowlist
// happens in registry.go, kept separate so this function stays testable
// against canned JSON without needing a real tailnet, and so a change to
// allowlist matching logic never risks touching the JSON-parsing code.
func QueryTailscalePeers() ([]LivePeer, error) {
	cmd := exec.Command("tailscale", "status", "--json")
	executil.HideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		// exec.ExitError means the command ran but exited non-zero (e.g.
		// tailscaled isn't running); anything else means we couldn't even
		// launch it (e.g. tailscale isn't installed / not on PATH). Worth
		// distinguishing in the error message since the fix is different.
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("tailscale status exited with error: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("running 'tailscale status --json': %w (is tailscale installed and on PATH?)", err)
	}

	var status tailscaleStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return nil, fmt.Errorf("parsing tailscale status JSON: %w", err)
	}

	now := time.Now()
	peers := make([]LivePeer, 0, len(status.Peer)+1)

	// Self is handled separately from the Peer loop below, and its Online
	// field is deliberately ignored rather than trusted. This isn't a
	// workaround for a bug (Tailscale did have one — issue #3564 — but it
	// was fixed in 2021, long before this was written; checked directly
	// rather than assumed). The real reasoning holds independent of
	// whether that field is ever accurate: if `tailscale status` just
	// returned data to this process, that alone proves this machine is
	// online — there's no informative sense in which Self.Online could
	// meaningfully disagree with a query that just succeeded. We already
	// have a strictly better signal than the field provides, so we use it
	// instead of reading a field we don't need.
	if selfHostname := shortHostname(status.Self.DNSName); selfHostname != "" {
		peers = append(peers, LivePeer{
			Hostname:    selfHostname,
			IPv4Address: firstIPv4(status.Self.TailscaleIPs),
			Online:      true,
			SeenAt:      now,
		})
	}

	for _, p := range status.Peer {
		hostname := shortHostname(p.DNSName)
		if hostname == "" {
			// A peer with no usable DNSName can't be matched to an
			// allowlist entry by hostname; skip it rather than returning a
			// LivePeer with an empty Hostname that could accidentally
			// match an equally-empty or malformed allowlist entry.
			continue
		}

		peers = append(peers, LivePeer{
			Hostname:    hostname,
			IPv4Address: firstIPv4(p.TailscaleIPs),
			Online:      p.Online,
			SeenAt:      now,
		})
	}

	return peers, nil
}

// shortHostname strips a Tailscale DNSName down to its first label, e.g.
// "ataraxia.tailnet-abc123.ts.net." becomes "ataraxia". This matches the
// hostname form used in node_allowlist.yaml.
func shortHostname(dnsName string) string {
	trimmed := strings.TrimSuffix(dnsName, ".")
	if trimmed == "" {
		return ""
	}
	parts := strings.SplitN(trimmed, ".", 2)
	return parts[0]
}

// firstIPv4 returns the first IPv4-looking address in the list, skipping
// IPv6 addresses (Tailscale peers typically have both). TailscaleIPs is
// documented as containing both v4 and v6 addresses for a node; this
// package only needs one address to reach the node's Agent/RPC ports, and
// v4 is the simpler, more predictable choice for that. A colon is a cheap,
// reliable enough IPv6 signal here — full address parsing isn't needed for
// just picking which one to keep.
func firstIPv4(ips []string) string {
	for _, ip := range ips {
		if !strings.Contains(ip, ":") {
			return ip
		}
	}
	return ""
}
