const fallbackDashboard = {
  source: "Recorded handoff snapshot; connect the dashboard API for live status.",
  observedAt: "2026-09-13",
  nodes: [
    {
      hostname: "mathesis",
      status: "verified",
      tailscaleIP: "100.114.155.22",
      agentPort: 7420,
      rpcPort: 50053,
      gpuModel: "NVIDIA GeForce RTX 3050 Laptop GPU",
      vramTotalBytes: 4294967296,
      vramFreeBytes: 3435973837,
      note: "CUDA RPC inference verified"
    }
  ],
  models: [
    {
      name: "TinyLlama 1.1B Chat v1.0 Q4_K_M",
      path: "~/models/tinyllama-1.1b-chat-v1.0.Q4_K_M.gguf",
      format: "GGUF",
      note: "Recorded inference model"
    }
  ]
};

const byteUnit = 1024 ** 3;
const byId = (id) => document.getElementById(id);

function stateClass(status) {
  const normalized = String(status || "unknown").toLowerCase();
  if (normalized === "online" || normalized === "verified") return "state--online";
  if (normalized === "offline" || normalized === "agentunreachable") return "state--offline";
  return "state--unknown";
}

function modelStateClass(state) {
  const value = String(state || "unloaded").toLowerCase();
  return ["unloaded", "loading", "loaded", "idle-countdown", "unloading"].includes(value) ? value : "unknown";
}

function setTheme(theme) {
  const isDark = theme === "dark";
  document.documentElement.dataset.theme = theme;
  document.querySelector('meta[name="theme-color"]').content = isDark ? "#000000" : "#ffffff";
  const toggle = byId("theme-toggle");
  toggle.textContent = isDark ? "Light mode" : "Dark mode";
  toggle.setAttribute("aria-pressed", String(isDark));
  toggle.setAttribute("aria-label", `Switch to ${isDark ? "light" : "dark"} mode`);
  localStorage.setItem("tether-theme", theme);
}

function initializeTheme() {
  const savedTheme = localStorage.getItem("tether-theme");
  const systemPrefersDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
  setTheme(savedTheme || (systemPrefersDark ? "dark" : "light"));
}

function gibibytes(bytes) {
  if (!Number.isFinite(bytes) || bytes <= 0) return "Not reported";
  const value = bytes / byteUnit;
  return `${value % 1 === 0 ? value : value.toFixed(1)} GiB`;
}

function displayTime(value, isFallback) {
  if (isFallback) return `Recorded ${value}`;
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? "Updated just now" : `Updated ${date.toLocaleString()}`;
}

async function fetchDashboard() {
  const response = await fetch("/api/v1/dashboard", { headers: { Accept: "application/json" } });
  if (!response.ok) throw new Error(`Dashboard API returned ${response.status}`);
  return response.json();
}

function renderNodes(nodes) {
  const body = byId("nodes-body");
  const empty = byId("node-empty");
  body.replaceChildren();
  empty.hidden = nodes.length !== 0;

  for (const node of nodes) {
    const row = document.createElement("tr");
    const status = String(node.status || "unknown");
    const endpoint = node.tailscaleIP && node.rpcPort ? `${node.tailscaleIP}:${node.rpcPort}` : "Not reported";
    const agentPort = node.agentPort ? `TCP ${node.agentPort}` : "Port not reported";
    const agent = node.agentStatus || agentPort;
    const role = node.isOrchestrator ? '<span class="node-role">Orchestrator</span>' : "";
    row.innerHTML = `
      <td>${escapeText(node.hostname)}${role}<span class="node-detail"><span class="state ${stateClass(status)}">${escapeText(status)}</span></span></td>
      <td>${escapeText(node.gpuModel || "Not reported")}<span class="node-detail">${escapeText(node.note || "")}</span></td>
      <td>${gibibytes(node.vramTotalBytes)}<span class="node-detail">${node.vramFreeBytes ? `${gibibytes(node.vramFreeBytes)} free` : "Free VRAM not reported"}</span></td>
      <td>${escapeText(endpoint)}</td>
      <td>${escapeText(agent)}<span class="node-detail">${escapeText(node.agentStatus ? agentPort : "")}</span></td>`;
    body.append(row);
  }
}

function renderModels(models) {
  const list = byId("models-list");
  const empty = byId("model-empty");
  list.replaceChildren();
  empty.hidden = models.length !== 0;

  for (const model of models) {
    const row = document.createElement("article");
    row.className = "model-row";
    const states = Array.isArray(model.states) ? model.states : [];
    const lifecycle = states.length
      ? states.map((state) => {
          const nodes = Array.isArray(state.nodes) && state.nodes.length ? state.nodes.join(", ") : "Not placed";
          const countdown = state.idleUntil ? ` · unloads ${new Date(state.idleUntil).toLocaleTimeString()}` : "";
          const label = String(state.state || "unloaded");
          return `<p class="model-state model-state--${modelStateClass(label)}">${escapeText(label)}<span>${escapeText(nodes + countdown)}</span></p>`;
        }).join("")
      : '<p class="model-state model-state--unknown">Not reported<span>Start tether-api to report worker state</span></p>';
    row.innerHTML = `
      <p class="model-name">${escapeText(model.name)}</p>
      <p class="model-path">${escapeText(model.path || "Path not reported")}</p>
      <p class="model-note">${escapeText([model.format, model.note].filter(Boolean).join(" · "))}</p>
      <div class="model-states" aria-label="Model lifecycle">${lifecycle}</div>`;
    list.append(row);
  }
}

function escapeText(value) {
  const span = document.createElement("span");
  span.textContent = value ?? "";
  return span.innerHTML;
}

function render(data, isFallback) {
  const nodes = Array.isArray(data.nodes) ? data.nodes : [];
  const models = Array.isArray(data.models) ? data.models : [];
  const totalVRAM = nodes.reduce((total, node) => total + (Number(node.vramTotalBytes) || 0), 0);
  const reportedFree = nodes.reduce((total, node) => total + (Number(node.vramFreeBytes) || 0), 0);
  const online = nodes.filter((node) => node.status === "online" || node.status === "verified").length;

  byId("node-count").textContent = String(nodes.length);
  byId("node-summary").textContent = isFallback ? `${nodes.length} recorded nodes — snapshot` : `${online} online or verified`;
  byId("vram-total").textContent = gibibytes(totalVRAM);
  byId("vram-summary").textContent = reportedFree ? `${gibibytes(reportedFree)} free reported` : "Free VRAM not reported";
  byId("model-count").textContent = String(models.length);
  byId("model-summary").textContent = models.length === 1 ? "GGUF model listed" : "GGUF models listed";
  byId("node-source").textContent = data.source || "Live Tether registry and Agent data";
  byId("model-source").textContent = data.modelSource || data.source || "Live orchestrator model inventory";
  byId("last-updated").textContent = displayTime(data.observedAt || new Date().toISOString(), isFallback);
  document.body.dataset.dataMode = isFallback ? "snapshot" : "live";
  byId("data-freshness").textContent = isFallback
    ? "Offline — showing a recorded handoff snapshot, not current cluster data. Use Tether desktop for operations."
    : "Live read-only diagnostic view. Use Tether desktop for operations.";
  renderNodes(nodes);
  renderModels(models);
}

async function refresh() {
  const button = byId("refresh");
  button.disabled = true;
  button.textContent = "Refreshing";
  try {
    render(await fetchDashboard(), false);
  } catch (error) {
    render(fallbackDashboard, true);
    byId("data-freshness").textContent = `Offline — ${error.message || 'the dashboard API could not be reached'}. Showing a recorded handoff snapshot, not current cluster data.`;
  } finally {
    button.disabled = false;
    button.textContent = "Refresh";
  }
}

byId("refresh").addEventListener("click", refresh);
byId("theme-toggle").addEventListener("click", () => {
  const nextTheme = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
  setTheme(nextTheme);
});
initializeTheme();
refresh();
