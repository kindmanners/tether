package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"tether/internal/agent"
	"tether/internal/certs"
	agentconfig "tether/internal/config"
	"tether/internal/pairing"
	"tether/internal/process"
	"tether/internal/registry"
	"tether/internal/trust"
	bootstrapassets "tether/scripts"
)

// AgentApp owns only this machine's setup and local command daemon. It never
// accepts a remote executable path or bind address: those remain local config
// written by the audited Windows provisioner.
type AgentApp struct {
	mu             sync.Mutex
	forcePairing   bool
	pairingActive  bool
	pairingCode    string
	serviceRunning bool
	serviceDetail  string
	serviceCancel  context.CancelFunc
	processManager *process.Manager
}

type AgentSetupState struct {
	Hostname          string `json:"hostname"`
	Platform          string `json:"platform"`
	ConfigPath        string `json:"configPath"`
	Configured        bool   `json:"configured"`
	ConfigurationNote string `json:"configurationNote"`
	BootstrapReady    bool   `json:"bootstrapReady"`
	BootstrapNote     string `json:"bootstrapNote"`
	Paired            bool   `json:"paired"`
	PairingActive     bool   `json:"pairingActive"`
	PairingCode       string `json:"pairingCode"`
	ServiceRunning    bool   `json:"serviceRunning"`
	ServiceDetail     string `json:"serviceDetail"`
	CanProvision      bool   `json:"canProvision"`
}

func NewAgentApp(forcePairing bool) *AgentApp {
	return &AgentApp{forcePairing: forcePairing, processManager: process.NewManager()}
}

func (a *AgentApp) startup(context.Context) {
	if a.forcePairing {
		go func() { _ = a.OpenPairing() }()
		return
	}
	go func() {
		state, err := a.State()
		if err == nil && state.Configured && state.Paired {
			_ = a.StartService()
		}
	}()
}

func (a *AgentApp) shutdown(context.Context) {
	a.mu.Lock()
	cancel := a.serviceCancel
	a.serviceCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	_ = a.processManager.StopDefault()
}

// State returns machine-local readiness only. The pairing code is exposed to
// the UI while its short-lived window is active and is neither persisted nor
// written to logs.
func (a *AgentApp) State() (*AgentSetupState, error) {
	state := &AgentSetupState{Platform: runtime.GOOS}
	hostname, hostnameErr := registry.SelfHostname()
	if hostnameErr != nil {
		state.ConfigurationNote = "Tailscale is required: " + hostnameErr.Error()
	} else {
		state.Hostname = hostname
		_, state.Paired, hostnameErr = trust.Get(orchestratorIdentityName)
		if hostnameErr != nil {
			state.ConfigurationNote = "Existing pairing needs attention: " + hostnameErr.Error()
		}
	}

	configPath, pathErr := agentconfig.DefaultPath()
	if pathErr != nil {
		return nil, fmt.Errorf("locating Agent configuration: %w", pathErr)
	}
	state.ConfigPath = configPath
	if _, err := agentconfig.Load(configPath); err != nil {
		state.ConfigurationNote = "Local RPC configuration is not ready: " + err.Error()
	} else {
		state.Configured = true
		if state.ConfigurationNote == "" {
			state.ConfigurationNote = "Local RPC configuration is ready."
		}
	}

	reportPath := filepath.Join(filepath.Dir(configPath), "bootstrap-report.json")
	if info, err := os.Stat(reportPath); err == nil && !info.IsDir() {
		state.BootstrapReady = true
		state.BootstrapNote = "GPU bootstrap report found at " + reportPath
	} else {
		state.BootstrapNote = "No bootstrap report yet. Run the local setup before pairing."
	}
	state.CanProvision = runtime.GOOS == "windows"

	a.mu.Lock()
	state.PairingActive = a.pairingActive
	state.PairingCode = a.pairingCode
	state.ServiceRunning = a.serviceRunning
	state.ServiceDetail = a.serviceDetail
	a.mu.Unlock()
	return state, nil
}

// StartProvisioning invokes the existing audited PowerShell provisioner with
// elevation. The script is intentionally responsible for installations,
// firewall changes and config writes; the UI does not reproduce that logic.
func (a *AgentApp) StartProvisioning() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("automatic GPU-node provisioning is currently available on Windows only")
	}
	script, agentPath, llamaPath, err := prepareWindowsProvisioner()
	if err != nil {
		return err
	}
	arguments := []string{
		"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script,
		"-Provision", "-InstallMissing", "-AgentExecutablePath", agentPath,
		"-LlamaCppPath", llamaPath,
	}
	quotedArguments := make([]string, len(arguments))
	for i, argument := range arguments {
		quotedArguments[i] = powerShellLiteral(argument)
	}
	command := "Start-Process -Verb RunAs -Wait -FilePath 'powershell.exe' -ArgumentList @(" + strings.Join(quotedArguments, ",") + ")"
	if err := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", command).Run(); err != nil {
		return fmt.Errorf("Windows setup did not complete: %w", err)
	}
	return nil
}

// OpenPairing starts one new, local pairing window. The generated code is
// displayed only in this GUI and the pairing server owns its two-minute TTL.
func (a *AgentApp) OpenPairing() error {
	a.mu.Lock()
	if a.pairingActive {
		a.mu.Unlock()
		return fmt.Errorf("a pairing window is already open")
	}
	if a.serviceRunning {
		a.mu.Unlock()
		return fmt.Errorf("stop the Agent service before opening a new pairing window")
	}
	a.mu.Unlock()

	hostname, err := registry.SelfHostname()
	if err != nil {
		return fmt.Errorf("checking Tailscale hostname: %w", err)
	}
	identity, err := certs.LoadOrCreate(hostname)
	if err != nil {
		return fmt.Errorf("loading local identity: %w", err)
	}
	server, err := pairing.NewServer(identity, pairing.Window)
	if err != nil {
		return fmt.Errorf("creating pairing window: %w", err)
	}

	a.mu.Lock()
	a.pairingActive = true
	a.pairingCode = server.Code()
	a.serviceDetail = "Waiting for the Orchestrator to use the displayed one-time code."
	a.mu.Unlock()
	go func() {
		err := server.Start(fmt.Sprintf(":%d", agentPort))
		a.mu.Lock()
		a.pairingActive = false
		a.pairingCode = ""
		if err != nil {
			a.serviceDetail = "Pairing did not complete: " + err.Error()
		} else {
			a.serviceDetail = "Pairing succeeded. Starting the local Agent service."
		}
		a.mu.Unlock()
		if err == nil {
			_ = a.StartService()
		}
	}()
	return nil
}

// StartService begins the pinned-mTLS command daemon after local setup and
// pairing are complete. It controls only its own process.Manager.
func (a *AgentApp) StartService() error {
	a.mu.Lock()
	if a.serviceRunning {
		a.mu.Unlock()
		return nil
	}
	// Reserve the start before performing filesystem and TLS checks so two UI
	// clicks (or a successful pairing plus a click) cannot race into two
	// listeners on the same Agent port.
	a.serviceRunning = true
	a.serviceDetail = "Starting the local Agent service."
	a.mu.Unlock()
	fail := func(err error) error {
		a.mu.Lock()
		a.serviceRunning = false
		a.serviceDetail = "Agent service did not start: " + err.Error()
		a.mu.Unlock()
		return err
	}

	hostname, err := registry.SelfHostname()
	if err != nil {
		return fail(fmt.Errorf("checking Tailscale hostname: %w", err))
	}
	identity, err := certs.LoadOrCreate(hostname)
	if err != nil {
		return fail(fmt.Errorf("loading local identity: %w", err))
	}
	paired, err := isPaired()
	if err != nil {
		return fail(fmt.Errorf("checking pairing: %w", err))
	}
	if !paired {
		return fail(fmt.Errorf("pair this machine with an Orchestrator first"))
	}
	configPath, err := agentconfig.DefaultPath()
	if err != nil {
		return fail(err)
	}
	cfg, err := agentconfig.Load(configPath)
	if err != nil {
		return fail(err)
	}
	tlsConfig, err := trust.PinnedTLSConfig(identity.TLSCertificate(), orchestratorIdentityName, true)
	if err != nil {
		return fail(fmt.Errorf("building pinned mTLS configuration: %w", err))
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.serviceCancel = cancel
	a.serviceDetail = fmt.Sprintf("Listening for the paired Orchestrator on TCP %d.", agentPort)
	a.mu.Unlock()
	go func() {
		err := agent.NewServer(cfg, a.processManager).Start(ctx, fmt.Sprintf(":%d", agentPort), tlsConfig)
		_ = a.processManager.StopDefault()
		a.mu.Lock()
		a.serviceRunning = false
		a.serviceCancel = nil
		if err != nil && ctx.Err() == nil {
			a.serviceDetail = "Agent service stopped: " + err.Error()
		} else if ctx.Err() != nil {
			a.serviceDetail = "Agent service stopped."
		}
		a.mu.Unlock()
	}()
	return nil
}

func (a *AgentApp) StopService() error {
	a.mu.Lock()
	cancel := a.serviceCancel
	a.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	return nil
}

// prepareWindowsProvisioner turns the one-file download into a self-contained
// local install. The embedded, reviewed bootstrap script and the currently
// running Agent are copied to a stable LocalAppData directory before the UAC
// prompt, so setup never depends on a source checkout or Downloads remaining
// in place.
func prepareWindowsProvisioner() (scriptPath, agentPath, llamaPath string, err error) {
	installRoot, err := os.UserCacheDir()
	if err != nil {
		return "", "", "", fmt.Errorf("locating local install directory: %w", err)
	}
	installRoot = filepath.Join(installRoot, "tether")
	if err := os.MkdirAll(installRoot, 0700); err != nil {
		return "", "", "", fmt.Errorf("creating local install directory: %w", err)
	}

	sourceAgent, err := os.Executable()
	if err != nil {
		return "", "", "", fmt.Errorf("locating Tether Agent: %w", err)
	}
	agentPath = filepath.Join(installRoot, "tether-agent.exe")
	if filepath.Clean(sourceAgent) != filepath.Clean(agentPath) {
		bytes, err := os.ReadFile(sourceAgent)
		if err != nil {
			return "", "", "", fmt.Errorf("reading Tether Agent for local install: %w", err)
		}
		if err := os.WriteFile(agentPath, bytes, 0700); err != nil {
			return "", "", "", fmt.Errorf("writing local Tether Agent: %w", err)
		}
	}

	scriptPath = filepath.Join(installRoot, "bootstrap-windows-node.ps1")
	if err := os.WriteFile(scriptPath, bootstrapassets.WindowsNodeBootstrap, 0600); err != nil {
		return "", "", "", fmt.Errorf("writing local setup script: %w", err)
	}
	return scriptPath, agentPath, filepath.Join(installRoot, "llama.cpp"), nil
}

func powerShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
