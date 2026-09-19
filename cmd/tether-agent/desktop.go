package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
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
	"tether/internal/heartbeat"
	"tether/internal/pairing"
	"tether/internal/process"
	"tether/internal/registry"
	"tether/internal/trust"
	bootstrapassets "tether/scripts"
)

// AgentApp owns only this machine's setup and local command daemon. It never
// accepts a remote executable path or bind address: those remain local config
// written only by the audited local provisioner.
type AgentApp struct {
	mu                 sync.Mutex
	forcePairing       bool
	pairingActive      bool
	pairingCode        string
	serviceRunning     bool
	serviceDetail      string
	serviceCancel      context.CancelFunc
	serviceDone        chan struct{}
	heartbeatDetail    string
	processManager     *process.Manager
	provisioning       bool
	provisioningDetail string
	provisioningError  string
	progressPath       string
	cancelPath         string
	logPath            string
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
	Hostname            string            `json:"hostname"`
	Platform            string            `json:"platform"`
	ConfigPath          string            `json:"configPath"`
	Configured          bool              `json:"configured"`
	ConfigurationNote   string            `json:"configurationNote"`
	BootstrapReady      bool              `json:"bootstrapReady"`
	BootstrapNote       string            `json:"bootstrapNote"`
	Paired              bool              `json:"paired"`
	PairingActive       bool              `json:"pairingActive"`
	PairingCode         string            `json:"pairingCode"`
	ServiceRunning      bool              `json:"serviceRunning"`
	ServiceDetail       string            `json:"serviceDetail"`
	HeartbeatDetail     string            `json:"heartbeatDetail"`
	CanProvision        bool              `json:"canProvision"`
	Ready               bool              `json:"ready"`
	Checklist           []AgentSetupCheck `json:"checklist"`
	Provisioning        bool              `json:"provisioning"`
	ProvisioningDetail  string            `json:"provisioningDetail"`
	ProvisioningError   string            `json:"provisioningError"`
	ProvisioningLog     string            `json:"provisioningLog"`
	ProvisioningLogPath string            `json:"provisioningLogPath"`
	CheckedAt           string            `json:"checkedAt"`
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
	checks := make([]AgentSetupCheck, 0, 12)
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
	if runtime.GOOS == "linux" && (!state.Configured || !state.BootstrapReady) {
		checks = append(checks, linuxPreflightChecks()...)
	}
	state.CanProvision = runtime.GOOS == "windows" || runtime.GOOS == "linux"
	state.Ready = hostname != "" && identityReady && state.Paired && state.Configured && state.BootstrapReady

	a.mu.Lock()
	state.PairingActive = a.pairingActive
	state.PairingCode = a.pairingCode
	state.ServiceRunning = a.serviceRunning
	state.ServiceDetail = a.serviceDetail
	state.HeartbeatDetail = a.heartbeatDetail
	state.Provisioning = a.provisioning
	state.ProvisioningDetail = a.provisioningDetail
	state.ProvisioningError = a.provisioningError
	progressPath := a.progressPath
	logPath := a.logPath
	a.mu.Unlock()
	if progressPath == "" {
		progressPath = defaultSetupProgressPath()
	}
	if logPath == "" {
		logPath = defaultSetupLogPath()
	}
	state.ProvisioningLogPath = logPath
	state.ProvisioningLog = readSetupLogTail(logPath)
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

// linuxPreflightChecks is deliberately read-only. The bootstrap script repeats
// these checks immediately before changing anything, so a stale UI result can
// never become an implicit authorization to provision a different machine.
func linuxPreflightChecks() []AgentSetupCheck {
	checks := make([]AgentSetupCheck, 0, 6)
	if path, err := exec.LookPath("nvidia-smi"); err != nil {
		checks = append(checks, setupCheck("nvidia", "Verify the NVIDIA driver and GPU", "nvidia-smi is not available. Install a supported NVIDIA driver before local setup.", false))
	} else if output, err := exec.Command(path, "-L").Output(); err != nil || strings.TrimSpace(string(output)) == "" {
		checks = append(checks, setupCheck("nvidia", "Verify the NVIDIA driver and GPU", "The NVIDIA driver did not report a usable GPU.", false))
	} else {
		checks = append(checks, setupCheck("nvidia", "Verify the NVIDIA driver and GPU", "Detected "+strings.TrimSpace(string(output))+".", true))
	}

	if path, err := exec.LookPath("nvcc"); err != nil {
		checks = append(checks, setupCheck("cuda", "Verify CUDA Toolkit availability", "nvcc is not available. Install the NVIDIA CUDA Toolkit before local setup.", false))
	} else if output, err := exec.Command(path, "--version").Output(); err != nil {
		checks = append(checks, setupCheck("cuda", "Verify CUDA Toolkit availability", "CUDA Toolkit could not be queried: "+err.Error(), false))
	} else {
		version := "CUDA Toolkit is available."
		if lines := strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' }); len(lines) > 0 {
			version = strings.TrimSpace(lines[len(lines)-1])
		}
		checks = append(checks, setupCheck("cuda", "Verify CUDA Toolkit availability", version, true))
	}

	missingTools := make([]string, 0, 3)
	for _, tool := range []string{"git", "cmake"} {
		if _, err := exec.LookPath(tool); err != nil {
			missingTools = append(missingTools, tool)
		}
	}
	if _, err := exec.LookPath("cc"); err != nil {
		if _, gccErr := exec.LookPath("gcc"); gccErr != nil {
			if _, clangErr := exec.LookPath("clang"); clangErr != nil {
				missingTools = append(missingTools, "a C/C++ compiler")
			}
		}
	}
	if len(missingTools) == 0 {
		checks = append(checks, setupCheck("build-tools", "Verify Git, CMake, and compiler", "Git, CMake, and a C/C++ compiler are available.", true))
	} else {
		checks = append(checks, setupCheck("build-tools", "Verify Git, CMake, and compiler", "Missing "+strings.Join(missingTools, ", ")+". The guided setup can use a supported distro package manager when you choose it.", false))
	}

	if tailscalePath, err := exec.LookPath("tailscale"); err != nil {
		checks = append(checks, setupCheck("tailscale-ip", "Verify the local Tailscale IPv4 address", "Tailscale is not available.", false))
	} else if output, err := exec.Command(tailscalePath, "ip", "-4").Output(); err != nil || strings.TrimSpace(string(output)) == "" {
		checks = append(checks, setupCheck("tailscale-ip", "Verify the local Tailscale IPv4 address", "Tailscale has no configured local IPv4 address.", false))
	} else {
		checks = append(checks, setupCheck("tailscale-ip", "Verify the local Tailscale IPv4 address", "Configured local Tailscale IPv4: "+strings.TrimSpace(string(output))+".", true))
	}

	for _, port := range []int{agentPort, 50053} {
		title := fmt.Sprintf("Verify TCP %d is available", port)
		if err := verifyTCPPortAvailable(port); err != nil {
			checks = append(checks, setupCheck(fmt.Sprintf("port-%d", port), title, err.Error(), false))
		} else {
			checks = append(checks, setupCheck(fmt.Sprintf("port-%d", port), title, "No existing listener was found.", true))
		}
	}
	return checks
}

// verifyTCPPortAvailable briefly asks the local kernel to reserve the selected
// port and immediately releases it. No listener remains and no network packet
// is sent; this is more reliable than assuming the optional ss utility exists.
func verifyTCPPortAvailable(port int) error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("TCP %d is already unavailable: %w", port, err)
	}
	return listener.Close()
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

// StartProvisioning invokes the audited, platform-specific local provisioner.
// The script is intentionally responsible for installations and config writes;
// the UI only launches it and presents its durable progress and log records.
func (a *AgentApp) StartProvisioning() error {
	switch runtime.GOOS {
	case "windows":
		return a.startWindowsProvisioning()
	case "linux":
		return a.startLinuxProvisioning()
	default:
		return fmt.Errorf("automatic GPU-node provisioning is currently available on Windows and Linux")
	}
}

func (a *AgentApp) startWindowsProvisioning() error {
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

func (a *AgentApp) startLinuxProvisioning() error {
	a.mu.Lock()
	if a.provisioning {
		a.mu.Unlock()
		return fmt.Errorf("local setup is already running")
	}
	a.mu.Unlock()
	script, err := prepareLinuxProvisioner()
	if err != nil {
		return err
	}
	installRoot := filepath.Dir(script)
	progressPath := filepath.Join(installRoot, "setup-progress.json")
	cancelPath := filepath.Join(installRoot, "setup-cancel.request")
	logPath := filepath.Join(installRoot, "setup.log")
	if err := os.Remove(cancelPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clearing a previous setup cancellation request: %w", err)
	}
	if err := writeSetupProgress(progressPath, setupProgress{Step: "requirements", Detail: "Preparing the audited Linux setup."}); err != nil {
		return err
	}
	if err := os.WriteFile(logPath, []byte("Tether Linux setup started. Full output will remain in this file.\n"), 0600); err != nil {
		return fmt.Errorf("creating persistent Linux setup log: %w", err)
	}
	a.mu.Lock()
	a.provisioning = true
	a.provisioningError = ""
	a.provisioningDetail = "Starting the audited Linux setup. Its current stage and persistent log appear below."
	a.progressPath = progressPath
	a.cancelPath = cancelPath
	a.logPath = logPath
	a.mu.Unlock()
	go a.runLinuxProvisioner(script, progressPath, cancelPath, logPath)
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

func (a *AgentApp) runLinuxProvisioner(script, progressPath, cancelPath, logPath string) {
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		defer logFile.Close()
		command := exec.Command("bash", script, "--provision", "--install-missing", "--progress-path", progressPath, "--cancel-path", cancelPath)
		command.Stdout = logFile
		command.Stderr = logFile
		err = command.Run()
	}
	a.mu.Lock()
	a.provisioning = false
	a.cancelPath = ""
	if err != nil {
		progress := readSetupProgress(progressPath)
		if progress.Step == "failed" && progress.Detail != "" {
			a.provisioningError = "Linux setup stopped: " + progress.Detail
		} else if progress.Step == "cancelled" && progress.Detail != "" {
			a.provisioningError = "Linux setup was cancelled: " + progress.Detail
		} else {
			a.provisioningError = "Linux setup did not complete: " + err.Error()
		}
		a.provisioningDetail = "Setup stopped. Read the persistent log below, correct the issue, then run setup again."
	} else {
		a.provisioningDetail = "Local Linux setup completed. Rechecking this machine now."
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

func defaultSetupLogPath() string {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cacheRoot, "tether", "setup.log")
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

func readSetupLogTail(path string) string {
	const maxBytes = 12 * 1024
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	if len(data) > maxBytes {
		data = append([]byte("… earlier setup output omitted …\n"), data[len(data)-maxBytes:]...)
	}
	return string(data)
}

func applySetupProgress(checks []AgentSetupCheck, progress setupProgress) {
	if progress.Step == "" {
		return
	}
	activeCheck := map[string]string{
		"requirements":  "local-setup",
		"tailscale":     "tailscale",
		"rpc-server":    "rpc-server",
		"firewall":      "local-setup",
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
			if saveErr := heartbeat.Save(server.OrchestratorTailnetHostname()); saveErr != nil {
				a.mu.Lock()
				a.serviceDetail = "Pairing succeeded, but saving the Orchestrator heartbeat target failed: " + saveErr.Error()
				a.mu.Unlock()
				return
			}
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
	a.serviceDone = make(chan struct{})
	a.serviceDetail = fmt.Sprintf("Listening for the paired Orchestrator on TCP %d.", agentPort)
	a.mu.Unlock()
	go a.runHeartbeat(ctx)
	go func() {
		err := agent.NewServer(cfg, a.processManager).Start(ctx, fmt.Sprintf(":%d", agentPort), tlsConfig)
		_ = a.processManager.StopDefault()
		a.mu.Lock()
		a.serviceRunning = false
		a.serviceCancel = nil
		if a.serviceDone != nil {
			close(a.serviceDone)
			a.serviceDone = nil
		}
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

// ResetPairing is an explicit local recovery action. It stops the mTLS
// listener before deleting both the old Agent identity and pinned
// Orchestrator certificate, then opens a fresh pairing window.
func (a *AgentApp) ResetPairing() error {
	a.mu.Lock()
	if a.provisioning || a.pairingActive {
		a.mu.Unlock()
		return fmt.Errorf("wait for the current setup or pairing action to finish")
	}
	cancel, done := a.serviceCancel, a.serviceDone
	a.mu.Unlock()
	if cancel != nil {
		cancel()
		if done != nil {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				return fmt.Errorf("the Agent service did not stop in time; try again")
			}
		}
	}
	hostname, err := registry.SelfHostname()
	if err != nil {
		return fmt.Errorf("checking Tailscale hostname: %w", err)
	}
	if err := trust.Delete(orchestratorIdentityName); err != nil {
		return err
	}
	if err := heartbeat.Delete(); err != nil {
		return err
	}
	if err := certs.Delete(hostname); err != nil {
		return err
	}
	a.mu.Lock()
	a.serviceDetail = "Previous pairing keys and certificates were removed. Creating a new pairing code."
	a.heartbeatDetail = "Heartbeat will resume after pairing completes."
	a.mu.Unlock()
	return a.OpenPairing()
}

func (a *AgentApp) runHeartbeat(ctx context.Context) {
	target, err := heartbeat.Load()
	if err != nil {
		a.mu.Lock()
		a.heartbeatDetail = "No paired Orchestrator heartbeat target is available."
		a.mu.Unlock()
		return
	}
	ping := func() {
		pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := heartbeat.Ping(pingCtx, target)
		cancel()
		a.mu.Lock()
		if err != nil {
			a.heartbeatDetail = "Orchestrator heartbeat failed: " + err.Error()
		} else {
			a.heartbeatDetail = "Orchestrator heartbeat to " + target + " succeeded at " + time.Now().Format(time.Kitchen)
		}
		a.mu.Unlock()
	}
	ping()
	ticker := time.NewTicker(heartbeat.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ping()
		}
	}
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

// prepareLinuxProvisioner keeps the reviewed script in a private user cache
// directory. Unlike the Windows path it does not copy or install the Agent:
// Linux package ownership remains with the user and the script only builds its
// local llama.cpp checkout plus the Agent's local configuration/report.
func prepareLinuxProvisioner() (string, error) {
	installRoot, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating local setup directory: %w", err)
	}
	installRoot = filepath.Join(installRoot, "tether")
	if err := os.MkdirAll(installRoot, 0700); err != nil {
		return "", fmt.Errorf("creating local setup directory: %w", err)
	}
	scriptPath := filepath.Join(installRoot, "bootstrap-linux-node.sh")
	if err := os.WriteFile(scriptPath, bootstrapassets.LinuxNodeBootstrap, 0700); err != nil {
		return "", fmt.Errorf("writing local Linux setup script: %w", err)
	}
	return scriptPath, nil
}

func powerShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
