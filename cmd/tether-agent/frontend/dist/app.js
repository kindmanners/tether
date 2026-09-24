/*
 * Copyright (C) 2026 kindmanners on github
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as
 * published by the Free Software Foundation, either version 3 of the
 * License, or (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

const api = () => window.go?.main?.AgentApp;
const notice = document.querySelector('#notice');
let refreshTimer;
let lastTelemetrySignature = '';
let lastChecklistSignature = '';

function formatBytes(bytes) {
  if (!Number.isFinite(bytes) || bytes <= 0) return '—';
  return `${(bytes / (1024 ** 3)).toFixed(bytes >= 100 * 1024 ** 3 ? 0 : 1)} GB`;
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[character]));
}

function renderTelemetry(gpus = [], modelStatus = '') {
  const signature = JSON.stringify({ gpus, modelStatus });
  if (signature === lastTelemetrySignature) return;
  lastTelemetrySignature = signature;
  const container = document.querySelector('#gpu-telemetry');
  container.replaceChildren();
  if (!gpus.length) {
    const empty = document.createElement('p');
    empty.className = 'telemetry-empty';
    empty.textContent = 'GPU telemetry appears here after audited local setup completes.';
    container.append(empty);
  }
  gpus.forEach((gpu, index) => {
    const total = Number(gpu.vramBytes) || 0;
    const free = Number(gpu.vramFreeBytes) || 0;
    const used = Math.max(0, total - free);
    const memoryPercent = total ? Math.min(100, Math.round((used / total) * 100)) : 0;
    const card = document.createElement('article');
    card.className = 'gpu-card';
    card.innerHTML = `<div class="gpu-card-heading"><p>GPU ${String(index + 1).padStart(2, '0')}</p><strong>${escapeHTML(gpu.name || 'NVIDIA GPU')}</strong></div>
      <div class="gpu-metrics"><div><span>GPU use</span><strong>${Number.isFinite(Number(gpu.utilizationPercent)) ? `${gpu.utilizationPercent}%` : '—'}</strong></div><div><span>VRAM</span><strong>${formatBytes(used)} <em>/ ${formatBytes(total)}</em></strong></div></div>
      <div class="capacity-bar" aria-label="${memoryPercent}% of VRAM in use"><span style="--usage: ${memoryPercent}%"></span></div>
      <p class="gpu-meta">${formatBytes(free)} free${gpu.driverVersion ? ` · Driver ${escapeHTML(gpu.driverVersion)}` : ''}</p>`;
    container.append(card);
  });
  document.querySelector('#model-status').textContent = modelStatus || 'Managed by the paired Orchestrator';
}

function setTheme(theme) {
  document.documentElement.dataset.theme = theme;
  const dark = theme === 'dark';
  const toggle = document.querySelector('#theme-toggle');
  toggle.textContent = dark ? 'Light mode' : 'Dark mode';
  toggle.setAttribute('aria-label', `Switch to ${dark ? 'light' : 'dark'} mode`);
  toggle.setAttribute('aria-pressed', String(dark));
  try { localStorage.setItem('tether-agent-theme', theme); } catch (_) { /* optional preference */ }
}

function report(message, error = false) {
  notice.textContent = message || '';
  notice.dataset.error = error ? 'true' : 'false';
}

function renderChecklist(checklist) {
  const signature = JSON.stringify(checklist);
  if (signature === lastChecklistSignature) return;
  lastChecklistSignature = signature;
  const list = document.querySelector('#checklist');
  list.replaceChildren();
  checklist.forEach((check, index) => {
    const item = document.createElement('li');
    item.className = `check ${check.status}`;
    item.style.setProperty('--index', index);

    const number = document.createElement('span');
    number.className = 'check-number';
    number.textContent = String(index + 1).padStart(2, '0');

    const content = document.createElement('div');
    content.className = 'check-content';
    const title = document.createElement('h3');
    title.textContent = check.title;
    const detail = document.createElement('p');
    detail.textContent = check.detail;
    content.append(title, detail);

    const status = document.createElement('span');
    status.className = 'status-label';
    status.textContent = check.status === 'finished' ? 'Finished' : check.status === 'started' ? 'In progress' : 'Not started';
    item.append(number, content, status);
    list.append(item);
  });

  const finished = checklist.filter(check => check.status === 'finished').length;
  const progress = checklist.length ? Math.round((finished / checklist.length) * 100) : 0;
  document.querySelector('#progress-count').textContent = `${finished}/${checklist.length}`;
  document.querySelector('#progress-caption').textContent = checklist.length ? `${progress}% complete` : 'No checks available';
  document.querySelector('#progress-bar').style.setProperty('--progress', `${progress}%`);
}

function updatePanels(state) {
  const localSetupComplete = state.configured && state.bootstrapReady;
  const setupPanel = document.querySelector('#setup-panel');
  setupPanel.hidden = localSetupComplete || !state.canProvision;
  setupPanel.classList.toggle('is-running', state.provisioning);
  document.querySelector('#ready-panel').hidden = !state.ready;
  document.querySelector('#pair-panel').hidden = state.ready || !localSetupComplete || state.paired;
  document.querySelector('#provision').disabled = state.provisioning;
  document.querySelector('#provision').textContent = state.provisioning ? 'Local setup is running…' : 'Run audited local setup';
  document.querySelector('#setup-description').textContent = state.platform === 'linux'
    ? 'The audited Linux setup repeats this read-only preflight, builds the pinned llama.cpp CUDA RPC server, writes only local Agent configuration and a capability report, and records every stage in a persistent log. It never pairs this node or changes firewall rules automatically.'
    : 'The audited Windows script checks prerequisites, handles Tailscale sign-in when needed, builds the pinned llama.cpp CUDA RPC server, adds Tailnet-only firewall rules, and writes the local Agent configuration.';

  const finishSetup = document.querySelector('#finish-setup');
  finishSetup.hidden = localSetupComplete || !state.canProvision;
  finishSetup.disabled = state.provisioning;
  finishSetup.textContent = state.provisioning ? 'Local setup is running…' : 'Finish local setup';

  document.querySelectorAll('#open-pair').forEach(button => {
    button.disabled = state.pairingActive || state.serviceRunning || !state.configured || !state.bootstrapReady;
  });
  document.querySelector('#code-panel').hidden = !state.pairingActive;
  document.querySelector('#pair-code').textContent = state.pairingCode || '';
  document.querySelector('#service-note').textContent = state.serviceDetail || 'The Agent will listen for commands from the paired Orchestrator.';
  document.querySelector('#heartbeat-note').textContent = state.heartbeatDetail || '';
  const resetPairing = document.querySelector('#reset-pairing');
  resetPairing.hidden = !state.paired;
  resetPairing.disabled = state.pairingActive || state.provisioning;

  const logPanel = document.querySelector('#setup-log-panel');
  const log = state.provisioningLog || '';
  logPanel.hidden = !log;
  document.querySelector('#setup-log').textContent = log;
  document.querySelector('#setup-log-path').textContent = state.provisioningLogPath || '';
}

async function refresh() {
  document.body.classList.add('is-refreshing');
  try {
    const state = await api().State();
    const localSetupComplete = state.configured && state.bootstrapReady;
    const headline = state.ready ? 'This GPU node is ready.' : state.provisioning ? 'Setting up this GPU node.' : localSetupComplete ? 'This GPU node is set up.' : 'This GPU node needs a few things.';
    document.querySelector('#headline').textContent = headline;
    document.querySelector('#machine-state').textContent = state.ready ? 'Ready' : state.provisioning ? 'Setup in progress' : localSetupComplete ? 'Pairing required' : 'Setup required';
    document.querySelector('#summary').textContent = state.ready ? 'No local setup is needed. Open Tether on the paired Orchestrator to use this GPU.' : state.provisioning ? (state.provisioningDetail || 'The audited setup is running. The active checklist item shows exactly what it is doing.') : localSetupComplete ? 'Audited local setup is complete. Pair this node with the Orchestrator to begin using its GPU.' : 'Nothing changes until you choose to run the audited local setup. Review the checklist first.';
    document.querySelector('#hostname').textContent = state.hostname || 'Tailscale unavailable';
    document.querySelector('#config').textContent = state.configurationNote;
    document.querySelector('#report').textContent = state.bootstrapNote;
    renderTelemetry(state.gpus, state.modelStatus);

    const checkedAt = new Date(state.checkedAt);
    const refreshLabel = state.provisioning ? 'Last live update' : 'Last checked';
    document.querySelector('#last-refreshed').textContent = Number.isNaN(checkedAt.getTime()) ? `${refreshLabel}: just now` : `${refreshLabel}: ${checkedAt.toLocaleTimeString()}`;
    document.querySelector('#telemetry-updated').textContent = Number.isNaN(checkedAt.getTime()) ? 'Updated just now' : `Updated ${checkedAt.toLocaleTimeString()}`;
    renderChecklist(state.checklist || []);
    updatePanels(state);
    if (state.provisioningError) report(state.provisioningError, true);
    else if (!state.provisioning) report('');
    clearTimeout(refreshTimer);
    if (state.provisioning || state.pairingActive) refreshTimer = setTimeout(refresh, 1000);
  } catch (error) {
    report(error.message || String(error), true);
    clearTimeout(refreshTimer);
  } finally {
    document.body.classList.remove('is-refreshing');
  }
}

document.querySelector('#refresh').addEventListener('click', refresh);
document.querySelector('#theme-toggle').addEventListener('click', () => {
  setTheme(document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark');
});
document.querySelector('#provision').addEventListener('click', async () => {
  try { await api().StartProvisioning(); await refresh(); }
  catch (error) { report(error.message || String(error), true); }
});
document.querySelector('#finish-setup').addEventListener('click', async () => {
  try { await api().StartProvisioning(); await refresh(); }
  catch (error) { report(error.message || String(error), true); }
});
document.querySelectorAll('#open-pair').forEach(button => button.addEventListener('click', async () => {
  try { await api().OpenPairing(); await refresh(); }
  catch (error) { report(error.message || String(error), true); }
}));
document.querySelector('#reset-pairing').addEventListener('click', async () => {
  if (!window.confirm('Re-pair with a different Orchestrator? This stops the Agent, deletes its current key, certificate, and saved Orchestrator trust record, then shows a new one-time code.')) return;
  try { await api().ResetPairing(); await refresh(); }
  catch (error) { report(error.message || String(error), true); }
});
window.addEventListener('DOMContentLoaded', () => {
  let theme = window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  try { theme = localStorage.getItem('tether-agent-theme') || theme; } catch (_) { /* optional preference */ }
  setTheme(theme);
  refresh();
});
