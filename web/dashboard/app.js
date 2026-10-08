/* Copyright (C) 2026 kindmanners on github — AGPL-3.0-or-later */

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
  document.querySelector('meta[name="theme-color"]').content = isDark ? "#07131f" : "#eef3f5";
  const toggle = byId("theme-toggle");
  toggle.querySelector(".theme-label").textContent = isDark ? "Light mode" : "Dark mode";
  toggle.setAttribute("aria-pressed", String(isDark));
  toggle.setAttribute("aria-label", `Switch to ${isDark ? "light" : "dark"} mode`);
  localStorage.setItem("tether-theme", theme);
}

function initializeTheme() {
  const savedTheme = localStorage.getItem("tether-theme");
  setTheme(savedTheme || "light");
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

function shortGPUName(value) {
  return String(value || "GPU not reported").replace(/^NVIDIA\s+(GeForce\s+)?/i, "");
}

function renderCapacity(nodes, totalVRAM, freeVRAM) {
  const usedVRAM = Math.max(0, totalVRAM - freeVRAM);
  const ratio = totalVRAM > 0 ? Math.min(82, Math.max(18, (usedVRAM / totalVRAM) * 100)) : 50;
  const visual = byId("capacity-visual");
  visual.style.setProperty("--allocated-ratio", `${ratio}%`);
  visual.setAttribute("aria-label", `${gibibytes(usedVRAM)} allocated and ${gibibytes(freeVRAM)} free across ${nodes.length} nodes`);
  byId("capacity-used-value").textContent = gibibytes(usedVRAM);
  byId("capacity-free-value").textContent = gibibytes(freeVRAM);
  byId("vram-used").textContent = gibibytes(usedVRAM);
  byId("vram-free").textContent = gibibytes(freeVRAM);

  const contributors = byId("capacity-contributors");
  contributors.replaceChildren();
  contributors.style.setProperty("--node-columns", Math.max(1, nodes.length));
  for (const node of nodes) {
    const item = document.createElement("p");
    item.className = "contributor";
    item.innerHTML = `<strong>${escapeText(node.hostname)}</strong> · ${escapeText(shortGPUName(node.gpuModel))} · ${escapeText(gibibytes(node.vramTotalBytes))}`;
    contributors.append(item);
  }
}

function renderNodes(nodes) {
  const body = byId("nodes-body");
  const empty = byId("node-empty");
  body.replaceChildren();
  empty.hidden = nodes.length !== 0;

  for (const node of nodes) {
    const row = document.createElement("tr");
    const status = String(node.status || "unknown");
    const endpoint = node.rpcPort ? `TCP ${node.rpcPort}` : "Not reported";
    const agentPort = node.agentPort ? `TCP ${node.agentPort}` : "Port not reported";
    const agent = node.agentStatus || agentPort;
    const role = node.isOrchestrator ? '<span class="node-role">Orchestrator</span>' : "";
    row.innerHTML = `
      <td data-label="Node">${escapeText(node.hostname)}${role}<span class="node-detail"><span class="state ${stateClass(status)}">${escapeText(status)}</span></span></td>
      <td data-label="GPU">${escapeText(node.gpuModel || "Not reported")}<span class="node-detail">${escapeText(node.note || "")}</span></td>
      <td data-label="VRAM">${gibibytes(node.vramTotalBytes)}<span class="node-detail">${node.vramFreeBytes ? `${gibibytes(node.vramFreeBytes)} free` : "Free VRAM not reported"}</span></td>
      <td data-label="Local RPC">${escapeText(endpoint)}</td>
      <td data-label="Agent">${escapeText(agent)}<span class="node-detail">${escapeText(node.agentStatus ? agentPort : "")}</span></td>`;
    body.append(row);
  }
}

function renderModels(models, modelStateError) {
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
      : `<p class="model-state model-state--unknown">Not reported<span>${escapeText(modelStateError || "Start tether-api with the dashboard API key to report worker state")}</span></p>`;
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
  byId("node-summary").textContent = isFallback ? `${nodes.length} recorded` : `${online} available`;
  byId("vram-total").textContent = gibibytes(totalVRAM);
  byId("vram-total-summary").textContent = gibibytes(totalVRAM);
  byId("vram-summary").textContent = reportedFree ? `${gibibytes(reportedFree)} free` : "Free not reported";
  byId("model-count").textContent = String(models.length);
  byId("model-summary").textContent = models.length === 1 ? "GGUF model" : "GGUF models";
  byId("pool-summary").textContent = totalVRAM > 0
    ? `${nodes.length} ${nodes.length === 1 ? "node contributes" : "nodes pool"} ${gibibytes(totalVRAM)} for local inference.`
    : `VRAM capacity has not been reported for ${nodes.length} ${nodes.length === 1 ? "node" : "nodes"}.`;
  byId("node-source").textContent = data.source || "Live Tether registry and Agent data";
  const modelSource = data.modelSource || data.source || "Live orchestrator model inventory";
  byId("model-source").textContent = data.modelStateError ? `${modelSource} · Worker state unavailable` : modelSource;
  const updated = displayTime(data.observedAt || new Date().toISOString(), isFallback);
  byId("last-updated").textContent = updated;
  byId("overview-time").textContent = updated;
  byId("overview-state").textContent = isFallback ? "Snapshot" : "Live";
  document.body.dataset.dataMode = isFallback ? "snapshot" : "live";
  byId("data-freshness").textContent = isFallback
    ? "Offline: showing a recorded handoff snapshot, not current cluster data. Use Tether desktop for operations."
    : data.modelStateError
      ? `Live cluster inventory; ${data.modelStateError}`
      : "Live read-only diagnostics. Use Tether desktop for operations.";

  renderCapacity(nodes, totalVRAM, reportedFree);
  renderNodes(nodes);
  renderModels(models, data.modelStateError);
}

async function refresh() {
  const button = byId("refresh");
  button.disabled = true;
  button.querySelector("span:last-child").textContent = "Refreshing";
  document.body.classList.add("is-refreshing");
  try {
    render(await fetchDashboard(), false);
  } catch (error) {
    render(fallbackDashboard, true);
    byId("data-freshness").textContent = `Offline: ${error.message || "the dashboard API could not be reached"}. Showing a recorded handoff snapshot, not current cluster data.`;
  } finally {
    window.setTimeout(() => document.body.classList.remove("is-refreshing"), 760);
    button.disabled = false;
    button.querySelector("span:last-child").textContent = "Refresh";
  }
}

byId("refresh").addEventListener("click", refresh);
byId("theme-toggle").addEventListener("click", () => {
  const nextTheme = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
  setTheme(nextTheme);
});

initializeTheme();
refresh();
