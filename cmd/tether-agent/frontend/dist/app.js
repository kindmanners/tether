const api = () => window.go?.main?.AgentApp;
const notice = document.querySelector('#notice');
let refreshTimer;

function report(message, error = false) { notice.textContent = message || ''; notice.dataset.error = error ? 'true' : 'false'; }
function renderChecklist(checklist) {
  const list = document.querySelector('#checklist'); list.replaceChildren();
  checklist.forEach((check, index) => {
    const item = document.createElement('li'); item.className = `check ${check.status}`;
    const marker = document.createElement('span'); marker.className = 'state-marker'; marker.setAttribute('aria-label', check.status === 'finished' ? 'Finished' : check.status === 'started' ? 'In progress' : 'Not started');
    const number = document.createElement('span'); number.className = 'check-number'; number.textContent = String(index + 1).padStart(2, '0');
    const content = document.createElement('div'); const title = document.createElement('h3'); title.textContent = check.title; const detail = document.createElement('p'); detail.textContent = check.detail; content.append(title, detail);
    const status = document.createElement('span'); status.className = 'status-label'; status.textContent = check.status === 'finished' ? 'Finished' : check.status === 'started' ? 'Started' : 'Not started';
    item.append(marker, number, content, status); list.append(item);
  });
}
function updatePanels(state) {
  const localSetupComplete = state.configured && state.bootstrapReady;
  document.querySelector('#setup-panel').hidden = localSetupComplete || !state.canProvision;
  document.querySelector('#ready-panel').hidden = !state.ready;
  document.querySelector('#pair-panel').hidden = state.ready || !localSetupComplete || state.paired;
  document.querySelector('#provision').disabled = state.provisioning;
  document.querySelector('#provision').textContent = state.provisioning ? 'Local setup is running…' : 'Run audited local setup';
  const finishSetup = document.querySelector('#finish-setup');
  finishSetup.hidden = localSetupComplete || !state.canProvision;
  finishSetup.disabled = state.provisioning;
  finishSetup.textContent = state.provisioning ? 'Local setup is running…' : 'Finish local setup';
  document.querySelectorAll('#open-pair').forEach(button => button.disabled = state.pairingActive || state.serviceRunning || !state.configured || !state.bootstrapReady);
  document.querySelector('#code-panel').hidden = !state.pairingActive;
  document.querySelector('#pair-code').textContent = state.pairingCode || '';
  document.querySelector('#service-note').textContent = state.serviceDetail || 'The Agent will listen for commands from the paired Orchestrator.';
}
async function refresh() {
  try {
    const state = await api().State();
    const localSetupComplete = state.configured && state.bootstrapReady;
    document.querySelector('#headline').textContent = state.ready ? 'This GPU node is ready.' : state.provisioning ? 'Setting up this GPU node.' : localSetupComplete ? 'This GPU node is set up.' : 'This GPU node needs a few things.';
    document.querySelector('#summary').textContent = state.ready ? 'No local setup is needed. Open Tether on the paired Orchestrator to use this GPU.' : state.provisioning ? (state.provisioningDetail || 'The audited setup is running. The active checklist item shows exactly what it is doing.') : localSetupComplete ? 'Audited local setup is complete. Pair this node with the Orchestrator to begin using its GPU.' : 'Nothing changes until you choose to run the audited local setup. Review the checklist first.';
    document.querySelector('#hostname').textContent = state.hostname || 'Tailscale unavailable'; document.querySelector('#config').textContent = state.configurationNote; document.querySelector('#report').textContent = state.bootstrapNote;
    const checkedAt = new Date(state.checkedAt);
    const refreshLabel = state.provisioning ? 'Last live update' : 'Last checked';
    document.querySelector('#last-refreshed').textContent = Number.isNaN(checkedAt.getTime()) ? `${refreshLabel}: just now` : `${refreshLabel}: ${checkedAt.toLocaleTimeString()}`;
    renderChecklist(state.checklist || []); updatePanels(state);
    if (state.provisioningError) report(state.provisioningError, true); else if (!state.provisioning) report('');
    clearTimeout(refreshTimer); if (state.provisioning || state.pairingActive) refreshTimer = setTimeout(refresh, 1000);
  } catch (error) { report(error.message || String(error), true); clearTimeout(refreshTimer); }
}
document.querySelector('#refresh').addEventListener('click', refresh);
document.querySelector('#provision').addEventListener('click', async () => { try { await api().StartProvisioning(); await refresh(); } catch (error) { report(error.message || String(error), true); } });
document.querySelector('#finish-setup').addEventListener('click', async () => { try { await api().StartProvisioning(); await refresh(); } catch (error) { report(error.message || String(error), true); } });
document.querySelectorAll('#open-pair').forEach(button => button.addEventListener('click', async () => { try { await api().OpenPairing(); await refresh(); } catch (error) { report(error.message || String(error), true); } }));
window.addEventListener('DOMContentLoaded', refresh);
