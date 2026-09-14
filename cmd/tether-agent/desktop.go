package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"tether/internal/agent"
	"tether/internal/certs"
	agentconfig "tether/internal/config"
	"tether/internal/executil"
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
	mu                 sync.Mutex
	forcePairing       bool
	pairingActive      bool
	pairingCode        string
	serviceRunning     bool
	serviceDetail      string
	serviceCancel      context.CancelFunc
	processManager     *process.Manager
	provisioning       bool
	provisioningDetail string
	provisioningError  string
	progressPath       string
	cancelPath         string
}

// AgentSetupCheck is one visible, read-only preflight result. Status is
// intentionally limited to the three states shown by the UI: not-started,
// started, and finished.
type AgentSetupCheck struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status string `json:"status"`
}

type AgentSetupState struct {
	Hostname           string            `json:"hostname"`
	Platform           string            `json:"platform"`
	ConfigPath         string            `json:"configPath"`
	Configured         bool              `json:"configured"`
	ConfigurationNote  string            `json:"configurationNote"`
	BootstrapReady     bool              `json:"bootstrapReady"`
	BootstrapNote      string            `json:"bootstrapNote"`
	Paired             bool              `json:"paired"`
	PairingActive      bool              `json:"pairingActive"`
	PairingCode        string            `json:"pairingCode"`
	ServiceRunning     bool              `json:"serviceRunning"`
	ServiceDetail      string            `json:"serviceDetail"`
	CanProvision       bool              `json:"canProvision"`
	Ready              bool              `json:"ready"`
	Checklist          []AgentSetupCheck `json:"checklist"`
	Provisioning       bool              `json:"provisioning"`
	ProvisioningDetail string            `json:"provisioningDetail"`
	ProvisioningError  string            `json:"provisioningError"`
	CheckedAt          string            `json:"checkedAt"`
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
		if err == nil && state.Ready {
			_ = a.StartService()
		}
	}()
}

func (a *AgentApp) shutdown(context.Context) {
	a.mu.Lock()
	cancel := a.serviceCancel
	a.serviceCancel = nil
	setupCancelPath := ""
	if a.provisioning {
		setupCancelPath = a.cancelPath
	}
	a.mu.Unlock()
	if setupCancelPath != "" {
		// The elevated setup script owns the compiler process. A small request
		// file lets it stop CMake/MSBuild and its children before this GUI exits.
		_ = os.WriteFile(setupCancelPath, []byte("cancelled\n"), 0600)
	}
	if cancel != nil {
		cancel()
	}
	_ = a.processManager.StopDefault()
}

// State returns machine-local readiness only. The pairing code is exposed to
// the UI while its short-lived window is active and is neither persisted nor
// written to logs.
func (a *AgentApp) State() (*AgentSetupState, error) {
	state := &AgentSetupState{Platform: runtime.GOOS, CheckedAt: time.Now().Format(time.RFC3339)}
	checks := make([]AgentSetupCheck, 0, 6)
	hostname, hostnameErr := registry.SelfHostname()
	if hostnameErr != nil {
		state.ConfigurationNote = "Tailscale is required: " + hostnameErr.Error()
		checks = append(checks, setupCheck("tailscale", "Connect this PC to Tailscale", "Tailscale is not ready: "+hostnameErr.Error(), false))
	} else {
		state.Hostname = hostname
		checks = append(checks, setupCheck("tailscale", "Connect this PC to Tailscale", "Connected as "+hostname+".", true))
	}

	identityReady := false
	if hostname == "" {
		checks = append(checks, setupCheck("identity", "Create this Agent's certificate", "Waiting for Tailscale so the Agent identity can be named.", false))
	} else if identity, err := certs.Load(hostname); err != nil {
		if os.IsNotExist(err) {
			checks = append(checks, setupCheck("identity", "Create this Agent's certificate", "Not created yet. Open the pairing window after local setup; Tether creates this certificate locally on this PC. Tailscale does not provide it.", false))
		} else {
			checks = append(checks, setupCheck("identity", "Verify this Agent's certificate", "The local certificate needs attention: "+err.Error(), false))
		}
	} else if now := time.Now(); now.Before(identity.Certificate.NotBefore) || now.After(identity.Certificate.NotAfter) {
		checks = append(checks, setupCheck("identity", "Verify this Agent's certificate", "The local certificate is outside its validity period.", false))
	} else {
		identityReady = true
		checks = append(checks, setupCheck("identity", "Verify this Agent's certificate", "Local certificate and private key are valid.", true))
	}

	if hostname == "" {
		checks = append(checks, setupCheck("trust", "Verify the paired Orchestrator", "Pairing can begin after Tailscale is connected.", false))
	} else {
		_, state.Paired, hostnameErr = trust.Get(orchestratorIdentityName)
		if hostnameErr != nil {
			state.ConfigurationNote = "Existing pairing needs attention: " + hostnameErr.Error()
			checks = append(checks, setupCheck("trust", "Verify the paired Orchestrator", "The saved trust record needs attention: "+hostnameErr.Error(), false))
		} else if !state.Paired {
			checks = append(checks, setupCheck("trust", "Verify the paired Orchestrator", "Not paired yet. Pair only after local setup is complete.", false))
		} else {
			checks = append(checks, setupCheck("trust", "Verify the paired Orchestrator", "The Orchestrator's certificate is pinned and valid.", true))
		}
	}

	configPath, pathErr := agentconfig.DefaultPath()
	if pathErr != nil {
		return nil, fmt.Errorf("locating Agent configuration: %w", pathErr)
	}
	state.ConfigPath = configPath
	cfg, configErr := agentconfig.Load(configPath)
	if configErr != nil {
		if state.ConfigurationNote == "" {
			state.ConfigurationNote = "Local RPC configuration is not ready: " + configErr.Error()
		}
		checks = append(checks, setupCheck("rpc-server", "Verify the llama.cpp CUDA RPC server", "No usable RPC server configuration: "+configErr.Error(), false))
	} else {
		state.Configured = true
		checks = append(checks, setupCheck("rpc-server", "Verify the llama.cpp CUDA RPC server", "Found "+cfg.RPCServerPath+".", true))
		if state.ConfigurationNote == "" {
			state.ConfigurationNote = "Local RPC configuration is ready."
		}
	}

	reportPath := filepath.Join(filepath.Dir(configPath), "bootstrap-report.json")
	reportReady, reportDetail := validateBootstrapReport(reportPath)
	if reportReady {
		state.BootstrapReady = true
		state.BootstrapNote = "GPU bootstrap report is valid at " + reportPath
		if state.Configured {
			checks = append(checks, setupCheck("local-setup", "Complete the local GPU setup", reportDetail, true))
		} else {
			checks = append(checks, setupCheck("local-setup", "Complete the local GPU setup", "The GPU report is present, but Agent configuration is missing. Finish local setup to write agent_config.yaml.", false))
		}
	} else {
		state.BootstrapNote = reportDetail
		checks = append(checks, setupCheck("local-setup", "Complete the local GPU setup", reportDetail, false))
	}
	state.CanProvision = runtime.GOOS == "windows"
	state.Ready = hostname != "" && identityReady && state.Paired && state.Configured && state.BootstrapReady

	a.mu.Lock()
	state.PairingActive = a.pairingActive
	state.PairingCode = a.pairingCode
	state.ServiceRunning = a.serviceRunning
	state.ServiceDetail = a.serviceDetail
	state.Provisioning = a.provisioning
	state.ProvisioningDetail = a.provisioningDetail
	state.ProvisioningError = a.provisioningError
	progressPath := a.progressPath
	a.mu.Unlock()
	if progressPath == "" {
		progressPath = defaultSetupProgressPath()
	}
	progress := readSetupProgress(progressPath)
	if state.Provisioning {
		applySetupProgress(checks, progress)
		if progress.Detail != "" {
			// The elevated script is the source of truth once it has started;
			// replace the initial UAC-wait message with its current phase.
			state.ProvisioningDetail = progress.Detail
		}
	}
	if progress.Step == "failed" || progress.Step == "cancelled" {
		prefix := "The last local setup attempt failed: "
		if progress.Step == "cancelled" {
			prefix = "The last local setup attempt was cancelled: "
		}
		state.ProvisioningError = prefix + progress.Detail
		if state.Provisioning {
			// A terminal progress record means the elevated script is done even
			// if Windows did not promptly return control to its launcher.
			state.Provisioning = false
			a.mu.Lock()
			a.provisioning = false
			a.mu.Unlock()
		}
	}
	state.Checklist = checks
	return state, nil
}

func setupCheck(id, title, detail string, finished bool) AgentSetupCheck {
	status := "not-started"
	if finished {
		status = "finished"
	}
	return AgentSetupCheck{ID: id, Title: title, Detail: detail, Status: status}
}

func validateBootstrapReport(path string) (bool, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "No GPU setup report yet. Run the audited local setup to install prerequisites, build llama.cpp, configure the Agent, and add firewall rules."
		}
		return false, "Cannot read the GPU setup report: " + err.Error()
	}
	var report agent.CapabilitiesResult
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}), &report); err != nil {
		return false, "The GPU setup report is invalid: " + err.Error()
	}
	if report.Hostname == "" || len(report.GPUs) == 0 {
		return false, "The GPU setup report is incomplete. Run local setup again."
	}
	return true, "The audited GPU setup report is present and records " + report.Hostname + "."
}

// StartProvisioning invokes the existing audited PowerShell provisioner with
// elevation. The script is intentionally responsible for installations,
// firewall changes and config writes; the UI does not reproduce that logic.
func (a *AgentApp) StartProvisioning() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("automatic GPU-node provisioning is currently available on Windows only")
	}
	a.mu.Lock()
	if a.provisioning {
		a.mu.Unlock()
		return fmt.Errorf("local setup is already running")
	}
	a.mu.Unlock()
	script, agentPath, llamaPath, err := prepareWindowsProvisioner()
	if err != nil {
		return err
	}
	progressPath := filepath.Join(filepath.Dir(script), "setup-progress.json")
	cancelPath := filepath.Join(filepath.Dir(script), "setup-cancel.request")
	if err := os.Remove(cancelPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clearing a previous setup cancellation request: %w", err)
	}
	if err := writeSetupProgress(progressPath, setupProgress{Step: "requirements", Detail: "Waiting for permission to run the audited setup."}); err != nil {
		return err
	}
	a.mu.Lock()
	a.provisioning = true
	a.provisioningError = ""
	a.provisioningDetail = "Waiting for the Windows permission prompt."
	a.progressPath = progressPath
	a.cancelPath = cancelPath
	a.mu.Unlock()
	go a.runWindowsProvisioner(script, agentPath, llamaPath, progressPath, cancelPath)
	return nil
}

func (a *AgentApp) runWindowsProvisioner(script, agentPath, llamaPath, progressPath, cancelPath string) {
	arguments := []string{
		"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script,
		"-Provision", "-InstallMissing", "-AgentExecutablePath", agentPath,
		"-LlamaCppPath", llamaPath, "-ProgressPath", progressPath, "-CancelPath", cancelPath,
	}
	quotedArguments := make([]string, len(arguments))
	for i, argument := range arguments {
		quotedArguments[i] = powerShellLiteral(argument)
	}
	command := "Start-Process -Verb RunAs -Wait -WindowStyle Hidden -FilePath 'powershell.exe' -ArgumentList @(" + strings.Join(quotedArguments, ",") + ")"
	launcher := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", command)
	executil.HideWindow(launcher)
	err := launcher.Run()
	a.mu.Lock()
	a.provisioning = false
	a.cancelPath = ""
	if err != nil {
		progress := readSetupProgress(progressPath)
		if progress.Step == "failed" && progress.Detail != "" {
			a.provisioningError = "Windows setup stopped: " + progress.Detail
		} else {
			a.provisioningError = "Windows setup did not complete: " + err.Error()
		}
		a.provisioningDetail = "Setup stopped. Read the error below, correct it, then run setup again."
	} else {
		a.provisioningDetail = "Local setup completed. Rechecking this machine now."
	}
	a.mu.Unlock()
	if err == nil {
		if state, stateErr := a.State(); stateErr == nil && state.Ready {
			_ = a.StartService()
		}
	}
}

type setupProgress struct {
	Step   string `json:"step"`
	Detail string `json:"detail"`
}

func writeSetupProgress(path string, progress setupProgress) error {
	data, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("encoding setup progress: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

func defaultSetupProgressPath() string {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cacheRoot, "tether", "setup-progress.json")
}

func readSetupProgress(path string) setupProgress {
	data, err := os.ReadFile(path)
	if err != nil {
		return setupProgress{}
	}
	var progress setupProgress
	if json.Unmarshal(data, &progress) != nil {
		return setupProgress{}
	}
	return progress
}

func applySetupProgress(checks []AgentSetupCheck, progress setupProgress) {
	if progress.Step == "" {
		return
	}
	activeCheck := map[string]string{
		"requirements":  "local-setup",
		"tailscale":     "tailscale",
		"rpc-server":    "rpc-server",
		"configuration": "local-setup",
	}[progress.Step]
	if activeCheck == "" {
		return
	}
	for index := range checks {
		if checks[index].ID != activeCheck {
			continue
		}
		checks[index].Status = "started"
		if progress.Detail != "" {
			checks[index].Detail = progress.Detail
		}
		return
	}
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
	state, err := a.State()
	if err != nil {
		return fmt.Errorf("running local preflight: %w", err)
	}
	if !state.Configured || !state.BootstrapReady {
		return fmt.Errorf("complete and pass local GPU setup before pairing this machine")
	}

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
	state, err := a.State()
	if err != nil {
		return fail(fmt.Errorf("running local preflight: %w", err))
	}
	if !state.Ready {
		return fail(fmt.Errorf("local preflight is not complete; review the checklist before starting the Agent"))
	}

	hostname, err := registry.SelfHostname()
	if err != nil {
		return fail(fmt.Errorf("checking Tailscale hostname: %w", err))
	}
	identity, err := certs.Load(hostname)
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
