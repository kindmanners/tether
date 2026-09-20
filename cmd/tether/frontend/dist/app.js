const api = () => window.go?.main?.OrchestratorApp;
const nodes = document.querySelector('#nodes');
const modelGrid = document.querySelector('#models');
const notice = document.querySelector('#notice');
const dialog = document.querySelector('#pair-dialog');
const addDialog = document.querySelector('#add-dialog');
let pairingHost = '';
let preparationRefresh;
let telemetryRefresh;
let celebratingHost = '';

function setTheme(theme) {
  const isDark = theme === 'dark';
  document.documentElement.dataset.theme = isDark ? 'dark' : 'light';
  const toggle = document.querySelector('#theme-toggle');
  toggle.setAttribute('aria-pressed', String(isDark));
  toggle.setAttribute('aria-label', `Switch to ${isDark ? 'light' : 'dark'} mode`);
  toggle.querySelector('.theme-label').textContent = isDark ? 'Light' : 'Dark';
  localStorage.setItem('tether-theme', isDark ? 'dark' : 'light');
}

function report(message, error = false) {
  notice.textContent = message || '';
  notice.dataset.error = error ? 'true' : 'false';
}

function escapeHTML(value) {
  const el = document.createElement('span');
  el.textContent = value || '';
  return el.innerHTML;
}

function stateClass(value) {
  return String(value || '').toLowerCase().replace(/[^a-z0-9]+/g, '-');
}

function percentage(value) {
  const number = Number(value);
  return Number.isFinite(number) ? Math.max(0, Math.min(100, Math.round(number))) : 0;
}

function wholeNumber(value) {
  const number = Number(value);
  return Number.isFinite(number) && number > 0 ? Math.round(number) : 0;
}

function relativeTime(timestamp) {
  const then = Date.parse(timestamp);
  if (!Number.isFinite(then)) return 'RECENTLY';
  const seconds = Math.max(0, Math.round((Date.now() - then) / 1000));
  if (seconds < 15) return 'JUST NOW';
  if (seconds < 60) return `${seconds}S AGO`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}M AGO`;
  return `${Math.round(minutes / 60)}H AGO`;
}

function nodeState(node) {
  const tailnet = String(node.tailnet || 'Unknown');
  if (tailnet !== 'Online' || !node.paired || !node.pingAt || node.agentStatus === 'Unreachable') {
    return `<span class="state ${stateClass(tailnet)}">${escapeHTML(tailnet)}</span>`;
  }
  return `<span class="state online"><i aria-hidden="true"></i>Online <span>${wholeNumber(node.pingMs)} ms · ${relativeTime(node.pingAt)}</span></span>`;
}

function usageHistory(node) {
  const history = Array.isArray(node.gpuHistory) ? node.gpuHistory.slice(-12) : [];
  if (!history.length) return '';
  const latest = history[history.length - 1];
  const bars = field => history.map(sample => `<i style="--value:${percentage(sample[field])}"></i>`).join('');
  return `<section class="node-history" aria-label="Recent GPU usage for ${escapeHTML(node.hostname)}">
    <span class="history-window">Last 60s</span>
    <div class="history-row"><span class="history-label gpu">GPU ${percentage(latest.gpuPercent)}%</span><span class="history-track gpu" aria-hidden="true">${bars('gpuPercent')}</span></div>
    <div class="history-row"><span class="history-label vram">VRAM ${percentage(latest.vramPercent)}%</span><span class="history-track vram" aria-hidden="true">${bars('vramPercent')}</span></div>
  </section>`;
}

function formatBytes(value) {
  const bytes = Number(value);
  if (!Number.isFinite(bytes) || bytes < 1) return '0 B';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  const exponent = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const amount = bytes / (1024 ** exponent);
  return `${amount >= 10 || exponent === 0 ? Math.round(amount) : amount.toFixed(1)} ${units[exponent]}`;
}

function renderModel(model) {
  const state = String(model.state || 'unloaded');
  const stateClassName = stateClass(state);
  const isLoaded = state === 'loaded' || state === 'idle-countdown';
  const placement = Array.isArray(model.nodes) && model.nodes.length ? model.nodes.join(' + ') : 'Placement selected when loaded';
  return `<article class="model-card" data-model="${escapeHTML(model.id)}">
    <div class="model-card-top"><div><h3>${escapeHTML(model.id)}</h3><p>${escapeHTML(model.filename)}</p></div><span class="model-state ${stateClassName}">${escapeHTML(state)}</span></div>
    <div class="model-meta"><span>${formatBytes(model.sizeBytes)}</span><span>${escapeHTML(placement)}</span></div>
    ${model.detail ? `<p class="model-detail">${escapeHTML(model.detail)}</p>` : ''}
    <div class="card-actions"><button data-load-model="${escapeHTML(model.id)}" ${isLoaded || state === 'loading' ? 'hidden' : ''}>Load model</button><button class="secondary" data-unload-model="${escapeHTML(model.id)}" ${isLoaded ? '' : 'hidden'}>Unload</button></div>
  </article>`;
}

function renderModelLibrary(library) {
  document.querySelector('#model-directory').textContent = library?.directory ? `Model library: ${library.directory}` : '';
  const download = library?.download || {};
  const downloadEl = document.querySelector('#model-download');
  if (download.state === 'downloading') {
    const progress = download.totalBytes > 0 ? `${formatBytes(download.bytes)} / ${formatBytes(download.totalBytes)}` : formatBytes(download.bytes);
    downloadEl.innerHTML = `<p class="model-download"><strong>Downloading ${escapeHTML(download.filename)}</strong><span>${progress}</span><small>${escapeHTML(download.detail || '')}</small></p>`;
  } else if (download.state === 'failed') {
    downloadEl.innerHTML = `<p class="model-download error"><strong>Download failed</strong><small>${escapeHTML(download.detail || '')}</small></p>`;
  } else if (download.state === 'complete') {
    downloadEl.innerHTML = `<p class="model-download"><strong>${escapeHTML(download.filename)} installed</strong><span>${formatBytes(download.bytes)}</span></p>`;
  } else {
    downloadEl.innerHTML = '';
  }
  const installed = Array.isArray(library?.models) ? library.models : [];
  modelGrid.innerHTML = installed.length ? installed.map(renderModel).join('') : '<p class="empty">No GGUF models are installed yet. Download gpt-oss-20b or add a GGUF file to this library.</p>';
  const downloadButton = document.querySelector('#download-gpt-oss');
  downloadButton.disabled = download.state === 'downloading' || installed.some(model => model.filename === 'gpt-oss-20b-MXFP4.gguf');
  downloadButton.textContent = download.state === 'downloading' ? 'Downloading gpt-oss-20b…' : installed.some(model => model.filename === 'gpt-oss-20b-MXFP4.gguf') ? 'gpt-oss-20b installed' : 'Download gpt-oss-20b';
}

function selectWorkspace(tab) {
  const modelsActive = tab === 'models';
  document.querySelector('#cluster-panel').hidden = modelsActive;
  document.querySelector('#models-panel').hidden = !modelsActive;
  document.querySelectorAll('.workspace-tab').forEach(button => button.setAttribute('aria-selected', String(button.dataset.tab === tab)));
  localStorage.setItem('tether-workspace-tab', tab);
}

async function refresh({ quiet = false } = {}) {
  window.clearTimeout(telemetryRefresh);
  if (!quiet) report('Refreshing live node state…');
  document.body.classList.add('is-refreshing');
  let state;
  let library;
  try {
    [state, library] = await Promise.all([api().Snapshot(), api().Models()]);
    const onlineCount = state.nodes.filter(node => node.tailnet === 'Online').length;
    const pairedCount = state.nodes.filter(node => node.paired).length;
    document.querySelector('#allowlist').textContent = state.allowlistPath ? `Allowlist: ${state.allowlistPath}` : '';
    document.querySelector('#node-count').textContent = `${state.nodes.length} ${state.nodes.length === 1 ? 'node' : 'nodes'}`;
    document.querySelector('#online-count').textContent = onlineCount;
    document.querySelector('#paired-count').textContent = pairedCount;
		document.body.classList.toggle('is-cluster-ready', pairedCount > 0 && state.backend.ready);

    const gateway = document.querySelector('.gateway');
    gateway.classList.toggle('is-running', state.gateway.running);
    gateway.classList.toggle('is-unavailable', !state.gateway.available);
    document.querySelector('#gateway-state').textContent = state.gateway.running ? 'Gateway running' : 'Gateway stopped';
    document.querySelector('#gateway-detail').textContent = state.gateway.detail;
    document.querySelector('#gateway-endpoint').textContent = state.gateway.endpoint;
    const startGateway = document.querySelector('#start-gateway');
    startGateway.hidden = state.gateway.running;
    startGateway.disabled = !state.gateway.available || !state.backend.ready;
    const contributing = state.contributeLocalGPU;
    const toggle = document.querySelector('#toggle-local-gpu');
    const prepare = document.querySelector('#prepare-backend');
    document.querySelector('#contribution-state').textContent = contributing ? 'Contributing this machine’s GPU' : 'Control-only mode';
    document.querySelector('#contribution-detail').textContent = state.backend.detail || 'Local backend state is not available.';
    toggle.textContent = contributing ? 'Use control-only mode' : 'Contribute this GPU';
    toggle.setAttribute('aria-pressed', String(contributing));
    toggle.disabled = state.backend.preparing;
    prepare.textContent = state.backend.preparing ? 'Preparing local backend…' : state.backend.ready ? 'Rebuild local backend' : contributing ? 'Prepare CUDA backend' : 'Prepare RPC backend';
    prepare.disabled = state.backend.preparing;
    document.querySelector('.gateway').classList.toggle('is-control-only', !contributing);

    nodes.innerHTML = state.nodes.length ? state.nodes.map((node, index) => `
		<article class="node-card ${node.hostname === celebratingHost ? 'just-paired' : ''}" style="--index:${index}" data-hostname="${escapeHTML(node.hostname)}" data-state="${stateClass(node.tailnet)}">
        <span class="node-index">${String(index + 1).padStart(2, '0')}</span>
        <div class="node-top">
          <div><h3>${escapeHTML(node.hostname)}</h3><p>${escapeHTML(node.address || 'No Tailnet address')}</p></div>
          ${nodeState(node)}
        </div>
        <p class="detail">${escapeHTML(node.detail || 'Ready for a command.')}</p>
        <dl class="node-facts">
          <div><dt>Trust</dt><dd>${node.paired ? 'Paired' : 'Needs pairing'}</dd></div>
          <div><dt>RPC server</dt><dd>${escapeHTML(node.agentStatus || 'Not checked')}</dd></div>
          <div><dt>Port</dt><dd>${wholeNumber(node.rpcPort) || '—'}</dd></div>
        </dl>
        ${usageHistory(node)}
        <div class="card-actions">
          ${!node.paired && node.tailnet === 'Online' ? `<button data-pair="${escapeHTML(node.hostname)}">Pair node</button>` : ''}
		  ${node.paired && node.tailnet === 'Online' ? `<button data-start="${escapeHTML(node.hostname)}" ${node.agentStatus === 'Running' ? 'disabled title="RPC server is already running"' : ''}>${node.agentStatus === 'Running' ? 'RPC running' : 'Start RPC'}</button><button class="secondary" data-stop="${escapeHTML(node.hostname)}">Stop RPC</button>` : ''}
        </div>
      </article>`).join('') : '<p class="empty">No allowlisted nodes were found. Check Tailscale and the allowlist path.</p>';
	if (celebratingHost) {
		window.setTimeout(() => { celebratingHost = ''; }, 1100);
	}
    renderModelLibrary(library);
    report('');
  } catch (error) {
    report(error.message || String(error), true);
  } finally {
    document.body.classList.remove('is-refreshing');
    window.clearTimeout(preparationRefresh);
    window.clearTimeout(telemetryRefresh);
    if (state?.backend?.preparing) preparationRefresh = window.setTimeout(refresh, 2000);
    else if (state) telemetryRefresh = window.setTimeout(() => refresh({ quiet: true }), 5000);
  }
}

nodes.addEventListener('click', async event => {
  const button = event.target.closest('button');
  if (!button) return;
  pairingHost = button.dataset.pair || '';
  if (pairingHost) {
    document.querySelector('#pair-title').textContent = pairingHost;
    document.querySelector('#pair-code').value = '';
    dialog.showModal();
    return;
  }
  try {
    button.disabled = true;
    report('Sending command…');
    if (button.dataset.start) await api().StartRPC(button.dataset.start);
    if (button.dataset.stop) await api().StopRPC(button.dataset.stop);
    await refresh();
  } catch (error) {
    report(error.message || String(error), true);
    button.disabled = false;
  }
});

modelGrid.addEventListener('click', async event => {
  const button = event.target.closest('button');
  if (!button) return;
  try {
    button.disabled = true;
    if (button.dataset.loadModel) {
      report(`Loading ${button.dataset.loadModel} across the available GPUs…`);
      await api().LoadModel(button.dataset.loadModel);
    }
    if (button.dataset.unloadModel) {
      report(`Unloading ${button.dataset.unloadModel}…`);
      await api().UnloadModel(button.dataset.unloadModel);
    }
    await refresh();
  } catch (error) {
    report(error.message || String(error), true);
    button.disabled = false;
  }
});

document.querySelector('#confirm-pair').addEventListener('click', async event => {
  event.preventDefault();
  try {
    await api().Pair(pairingHost, document.querySelector('#pair-code').value.trim());
    dialog.close();
	celebratingHost = pairingHost;
	report(`${pairingHost} paired securely. Control is now enabled.`);
    await refresh();
  } catch (error) { report(error.message || String(error), true); }
});

document.querySelector('#add-node').addEventListener('click', async () => {
  try {
    const candidates = await api().TailnetCandidates();
    const select = document.querySelector('#candidate-select');
    select.innerHTML = candidates.length ? candidates.map(candidate => `<option value="${escapeHTML(candidate.hostname)}">${escapeHTML(candidate.hostname)} — ${escapeHTML(candidate.address)}</option>`).join('') : '<option value="">No unallowlisted online Tailnet machines</option>';
    document.querySelector('#confirm-add').disabled = !candidates.length;
    addDialog.showModal();
  } catch (error) { report(error.message || String(error), true); }
});

document.querySelector('#confirm-add').addEventListener('click', async event => {
  event.preventDefault();
  try {
    const hostname = document.querySelector('#candidate-select').value;
    const port = Number.parseInt(document.querySelector('#candidate-port').value, 10);
    await api().AddNode(hostname, Number.isNaN(port) ? 0 : port);
    addDialog.close();
    await refresh();
  } catch (error) { report(error.message || String(error), true); }
});

document.querySelector('#refresh').addEventListener('click', refresh);
document.querySelector('#refresh-models').addEventListener('click', refresh);
document.querySelector('#download-gpt-oss').addEventListener('click', async () => {
  try {
    await api().DownloadGPTOSS20B();
    report('Downloading gpt-oss-20b into the local model library…');
    await refresh({ quiet: true });
  } catch (error) { report(error.message || String(error), true); }
});
document.querySelectorAll('.workspace-tab').forEach(button => button.addEventListener('click', () => selectWorkspace(button.dataset.tab)));
document.querySelector('#theme-toggle').addEventListener('click', () => {
  setTheme(document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark');
});
document.querySelector('#start-gateway').addEventListener('click', async () => {
  try {
    await api().StartGateway();
    await refresh();
  } catch (error) { report(error.message || String(error), true); }
});
document.querySelector('#toggle-local-gpu').addEventListener('click', async () => {
  try {
    const enabled = document.querySelector('#toggle-local-gpu').getAttribute('aria-pressed') !== 'true';
    report('Updating this Orchestrator’s GPU role…');
    await api().SetLocalGPUContribution(enabled);
    await refresh();
  } catch (error) { report(error.message || String(error), true); }
});
document.querySelector('#prepare-backend').addEventListener('click', async () => {
  try {
    await api().PrepareLocalBackend();
    await refresh();
  } catch (error) { report(error.message || String(error), true); }
});
window.addEventListener('DOMContentLoaded', () => {
  const savedTheme = localStorage.getItem('tether-theme');
  const systemPrefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  setTheme(savedTheme || (systemPrefersDark ? 'dark' : 'light'));
  selectWorkspace(localStorage.getItem('tether-workspace-tab') || 'cluster');
  refresh();
});
