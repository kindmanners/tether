const api = () => window.go?.main?.OrchestratorApp;
const nodes = document.querySelector('#nodes');
const notice = document.querySelector('#notice');
const dialog = document.querySelector('#pair-dialog');
const addDialog = document.querySelector('#add-dialog');
let pairingHost = '';
let preparationRefresh;

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

async function refresh() {
  report('Refreshing live node state…');
  document.body.classList.add('is-refreshing');
  let state;
  try {
    state = await api().Snapshot();
    const onlineCount = state.nodes.filter(node => node.tailnet === 'Online').length;
    const pairedCount = state.nodes.filter(node => node.paired).length;
    document.querySelector('#allowlist').textContent = state.allowlistPath ? `Allowlist: ${state.allowlistPath}` : '';
    document.querySelector('#node-count').textContent = `${state.nodes.length} ${state.nodes.length === 1 ? 'node' : 'nodes'}`;
    document.querySelector('#online-count').textContent = onlineCount;
    document.querySelector('#paired-count').textContent = pairedCount;

    const gateway = document.querySelector('.gateway');
    gateway.classList.toggle('is-running', state.gateway.running);
    gateway.classList.toggle('is-unavailable', !state.gateway.available);
    document.querySelector('#gateway-state').textContent = state.gateway.running ? 'Gateway running' : 'Gateway stopped';
    document.querySelector('#gateway-detail').textContent = state.gateway.detail;
    document.querySelector('#gateway-endpoint').textContent = state.gateway.endpoint;
    document.querySelector('#start-gateway').disabled = state.gateway.running || !state.gateway.available || !state.backend.ready;
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
      <article class="node-card" style="--index:${index}" data-state="${stateClass(node.tailnet)}">
        <span class="node-index">${String(index + 1).padStart(2, '0')}</span>
        <div class="node-top">
          <div><h3>${escapeHTML(node.hostname)}</h3><p>${escapeHTML(node.address || 'No Tailnet address')}</p></div>
          <span class="state ${stateClass(node.tailnet)}">${escapeHTML(node.tailnet)}</span>
        </div>
        <dl class="node-facts">
          <div><dt>Trust</dt><dd>${node.paired ? 'Paired' : 'Needs pairing'}</dd></div>
          <div><dt>RPC server</dt><dd>${escapeHTML(node.agentStatus || 'Not checked')}</dd></div>
          <div><dt>Port</dt><dd>${node.rpcPort}</dd></div>
        </dl>
        <p class="detail">${escapeHTML(node.detail || 'Ready for a command.')}</p>
        <div class="card-actions">
          ${!node.paired && node.tailnet === 'Online' ? `<button data-pair="${escapeHTML(node.hostname)}">Pair node</button>` : ''}
          ${node.paired && node.tailnet === 'Online' ? `<button data-start="${escapeHTML(node.hostname)}">Start RPC</button><button class="secondary" data-stop="${escapeHTML(node.hostname)}">Stop RPC</button>` : ''}
        </div>
      </article>`).join('') : '<p class="empty">No allowlisted nodes were found. Check Tailscale and the allowlist path.</p>';
    report('');
  } catch (error) {
    report(error.message || String(error), true);
  } finally {
    document.body.classList.remove('is-refreshing');
    window.clearTimeout(preparationRefresh);
    if (state?.backend?.preparing) preparationRefresh = window.setTimeout(refresh, 2000);
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

document.querySelector('#confirm-pair').addEventListener('click', async event => {
  event.preventDefault();
  try {
    await api().Pair(pairingHost, document.querySelector('#pair-code').value.trim());
    dialog.close();
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
window.addEventListener('DOMContentLoaded', refresh);
