const api = () => window.go?.main?.OrchestratorApp;
const nodes = document.querySelector('#nodes');
const notice = document.querySelector('#notice');
const dialog = document.querySelector('#pair-dialog');
const addDialog = document.querySelector('#add-dialog');
let pairingHost = '';

function report(message, error = false) { notice.textContent = message || ''; notice.dataset.error = error ? 'true' : 'false'; }
function escapeHTML(value) { const el = document.createElement('span'); el.textContent = value || ''; return el.innerHTML; }

async function refresh() {
  report('Refreshing live node state…');
  try {
    const state = await api().Snapshot();
    document.querySelector('#allowlist').textContent = state.allowlistPath;
    document.querySelector('#gateway-state').textContent = state.gateway.running ? 'Gateway running' : 'Gateway stopped';
    document.querySelector('#gateway-detail').textContent = state.gateway.detail;
    document.querySelector('#gateway-endpoint').textContent = state.gateway.endpoint;
    document.querySelector('#start-gateway').disabled = state.gateway.running || !state.gateway.available;
    nodes.innerHTML = state.nodes.length ? state.nodes.map(node => `
      <article class="node-card">
        <div class="node-top"><div><h2>${escapeHTML(node.hostname)}</h2><p>${escapeHTML(node.address || 'No tailnet address')}</p></div><span class="state ${node.tailnet.toLowerCase()}">${escapeHTML(node.tailnet)}</span></div>
        <dl><div><dt>Trust</dt><dd>${node.paired ? 'Paired' : 'Needs pairing'}</dd></div><div><dt>RPC server</dt><dd>${escapeHTML(node.agentStatus || 'Not checked')}</dd></div><div><dt>Port</dt><dd>${node.rpcPort}</dd></div></dl>
        <p class="detail">${escapeHTML(node.detail || 'Ready for a command.')}</p>
        <div class="card-actions">${!node.paired && node.tailnet === 'Online' ? `<button data-pair="${escapeHTML(node.hostname)}">Pair node</button>` : ''}${node.paired && node.tailnet === 'Online' ? `<button data-start="${escapeHTML(node.hostname)}">Start RPC</button><button class="secondary" data-stop="${escapeHTML(node.hostname)}">Stop RPC</button>` : ''}</div>
      </article>`).join('') : '<p class="empty">No allowlisted nodes were found. Check Tailscale and the allowlist path.</p>';
    report('');
  } catch (error) { report(error.message || String(error), true); }
}

nodes.addEventListener('click', async event => {
  const button = event.target.closest('button'); if (!button) return;
  pairingHost = button.dataset.pair || '';
  if (pairingHost) { document.querySelector('#pair-title').textContent = pairingHost; document.querySelector('#pair-code').value = ''; dialog.showModal(); return; }
  try { button.disabled = true; report('Sending command…'); if (button.dataset.start) await api().StartRPC(button.dataset.start); if (button.dataset.stop) await api().StopRPC(button.dataset.stop); await refresh(); } catch (error) { report(error.message || String(error), true); button.disabled = false; }
});
document.querySelector('#confirm-pair').addEventListener('click', async event => { event.preventDefault(); try { await api().Pair(pairingHost, document.querySelector('#pair-code').value.trim()); dialog.close(); await refresh(); } catch (error) { report(error.message || String(error), true); } });
document.querySelector('#add-node').addEventListener('click', async () => { try { const candidates = await api().TailnetCandidates(); const select = document.querySelector('#candidate-select'); select.innerHTML = candidates.length ? candidates.map(candidate => `<option value="${escapeHTML(candidate.hostname)}">${escapeHTML(candidate.hostname)} — ${escapeHTML(candidate.address)}</option>`).join('') : '<option value="">No unallowlisted online Tailnet machines</option>'; document.querySelector('#confirm-add').disabled = !candidates.length; addDialog.showModal(); } catch (error) { report(error.message || String(error), true); } });
document.querySelector('#confirm-add').addEventListener('click', async event => { event.preventDefault(); try { const hostname = document.querySelector('#candidate-select').value; const port = Number.parseInt(document.querySelector('#candidate-port').value, 10); await api().AddNode(hostname, Number.isNaN(port) ? 0 : port); addDialog.close(); await refresh(); } catch (error) { report(error.message || String(error), true); } });
document.querySelector('#refresh').addEventListener('click', refresh);
document.querySelector('#start-gateway').addEventListener('click', async () => { try { await api().StartGateway(); await refresh(); } catch (error) { report(error.message || String(error), true); } });
window.addEventListener('DOMContentLoaded', refresh);
