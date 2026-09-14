// Command tether-agent is the Tether Agent — a lightweight daemon that
// runs on every machine contributing GPU to the cluster. It pairs with the
// Orchestrator once, then listens for pinned-mTLS commands to start, stop,
// and inspect the local llama.cpp rpc-server.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"tether/internal/agent"
	"tether/internal/certs"
	agentconfig "tether/internal/config"
	"tether/internal/pairing"
	"tether/internal/process"
	"tether/internal/registry"
	"tether/internal/trust"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// agentPort is shared by the short-lived pairing server and the persistent
// command server. Pairing releases the listener before the command server is
// started, so there is never more than one protocol listening on this port.
const agentPort = 7420

//go:embed all:frontend/dist
var desktopAssets embed.FS

// orchestratorIdentityName must match cmd/tether's identity name. Pairing
// pins that certificate under this name, which makes it both the marker that
// pairing has completed and the exact peer expected by the command server.
const orchestratorIdentityName = "orchestrator"

func main() {
	forcePairing := flag.Bool("pair", false, "open a new pairing window before serving commands")
	flag.Parse()
	app := NewAgentApp(*forcePairing)
	// Run the read-only readiness audit before Wails creates a window. This
	// never installs software, writes an identity, or starts the Agent.
	if _, err := app.State(); err != nil {
		log.Printf("tether-agent: preflight could not complete: %v", err)
	}
	if err := wails.Run(&options.App{
		Title:     "Tether Agent",
		Width:     970,
		Height:    700,
		MinWidth:  760,
		MinHeight: 560,
		AssetServer: &assetserver.Options{
			Assets: desktopAssets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind:       []interface{}{app},
	}); err != nil {
		log.Fatal(err)
	}
}

// runTerminalAgent remains available for support and tests of the original
// command flow. Normal tether-agent launches now use the desktop onboarding
// shell above.
func runTerminalAgent(forcePairing bool) {

	hostname, err := registry.SelfHostname()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tether-agent: could not determine this machine's Tailscale hostname: %v\n", err)
		fmt.Fprintln(os.Stderr, "tether-agent: is Tailscale installed and running on this machine?")
		os.Exit(1)
	}

	// Identity is keyed by the Tailscale hostname so its certificate name
	// always matches the name the Orchestrator uses for this same machine.
	identity, err := certs.LoadOrCreate(hostname)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tether-agent: could not load or create identity for %q: %v\n", hostname, err)
		os.Exit(1)
	}

	paired := false
	if !forcePairing {
		paired, err = isPaired()
		if err != nil {
			fmt.Fprintf(os.Stderr, "tether-agent: could not use existing pairing: %v\n", err)
			fmt.Fprintln(os.Stderr, "tether-agent: run with -pair to replace the existing pin.")
			os.Exit(1)
		}
	}
	if !paired || forcePairing {
		if err := pair(identity, hostname); err != nil {
			fmt.Fprintf(os.Stderr, "tether-agent: pairing did not complete: %v\n", err)
			fmt.Fprintln(os.Stderr, "tether-agent: run tether-agent again to try once more.")
			os.Exit(1)
		}
		fmt.Println()
		fmt.Println("  Pairing succeeded. Starting the command server.")
	}

	if err := serve(identity, hostname); err != nil {
		fmt.Fprintf(os.Stderr, "tether-agent: command server stopped: %v\n", err)
		os.Exit(1)
	}
}

// isPaired reports whether this Agent has a usable pin for its one expected
// Orchestrator. trust.Get also checks certificate validity, so an expired pin
// is not treated as an established pairing that could start a dead command
// channel.
func isPaired() (bool, error) {
	_, found, err := trust.Get(orchestratorIdentityName)
	if err != nil {
		return false, err
	}
	return found, nil
}

func pair(identity *certs.Identity, hostname string) error {
	server, err := pairing.NewServer(identity, pairing.Window)
	if err != nil {
		return fmt.Errorf("starting pairing session: %w", err)
	}

	fmt.Println()
	fmt.Println("=========================================")
	fmt.Printf("  Tether Agent — %s\n", hostname)
	fmt.Println("=========================================")
	fmt.Println()
	fmt.Println("  Ready to pair with the Orchestrator.")
	fmt.Println()
	fmt.Printf("  Pairing code:  %s\n", server.Code())
	fmt.Println()
	fmt.Printf("  Enter this code in the Orchestrator within %s.\n", pairing.Window)
	fmt.Println("  This code is single-use and shown only here — it is never logged.")
	fmt.Println()

	return server.Start(fmt.Sprintf(":%d", agentPort))
}

func serve(identity *certs.Identity, hostname string) error {
	configPath, err := agentconfig.DefaultPath()
	if err != nil {
		return fmt.Errorf("locating agent config: %w", err)
	}
	cfg, err := agentconfig.Load(configPath)
	if err != nil {
		return err
	}

	tlsConfig, err := trust.PinnedTLSConfig(
		identity.TLSCertificate(),
		orchestratorIdentityName,
		true,
	)
	if err != nil {
		return fmt.Errorf("building pinned mTLS configuration: %w", err)
	}

	manager := process.NewManager()
	server := agent.NewServer(cfg, manager)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := fmt.Sprintf(":%d", agentPort)
	fmt.Printf("Tether Agent — %s\n", hostname)
	fmt.Printf("Command server listening on %s; press Ctrl-C to stop.\n", addr)

	err = server.Start(ctx, addr, tlsConfig)
	if stopErr := manager.StopDefault(); stopErr != nil {
		return fmt.Errorf("stopping managed rpc-server: %w", stopErr)
	}
	return err
}
