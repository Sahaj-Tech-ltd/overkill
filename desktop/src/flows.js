(function(root) {
  'use strict';
  function create({api, document, getSession, canRefresh, refreshHistory}) {
    let selected = null, fingerprint = null, generation = 0, pending = false;
    const region = document.getElementById('flow-status');
    function reset() { generation++; selected = null; fingerprint = null; region.hidden = true; region.replaceChildren(); }
    async function sync() {
      const id = getSession()?.id;
      if (!id || !canRefresh()) return;
      if (selected !== id) { reset(); selected = id; }
      if (pending) return;
      const epoch = generation;
      pending = true;
      try {
        const result = await api.flows(id);
        if (generation !== epoch || getSession()?.id !== id || !canRefresh()) return;
        const flows = Array.isArray(result.flows) ? result.flows.slice(0, 100) : [];
        const next = JSON.stringify(flows);
        if (next === fingerprint) return;
        const active = flows.find(flow => ['running', 'queued'].includes(flow.status)) || flows[0];
        region.replaceChildren(); region.hidden = !active;
        if (active) {
          const text = document.createElement('span');
          const labels = {queued: 'Continuation queued', running: 'Continuing saved work', completed: 'Saved work completed', paused: 'Continuation paused', manual_review: 'Continuation needs review; it will not replay automatically', exhausted: 'Continuation limit reached', cancelled: 'Continuation cancelled', failed: 'Continuation failed'};
          text.textContent = labels[active.status] || 'Saved work status unavailable';
          if (active.status === 'queued' && Number.isFinite(Date.parse(active.due_at))) text.textContent += ' · ' + new Date(active.due_at).toLocaleTimeString();
          region.append(text);
          if (['queued', 'running'].includes(active.status)) {
            const pause = document.createElement('button'); pause.type = 'button'; pause.textContent = 'Pause continuation';
            pause.addEventListener('click', async () => {
              if (getSession()?.id !== id) return;
              pause.disabled = true;
              try { await api.stop(id); fingerprint = null; await sync(); }
              catch { if (generation === epoch) text.textContent = 'Could not pause continuation. Refresh its status before retrying.'; }
              finally { pause.disabled = false; }
            });
            region.append(pause);
          }
        }
        // Refresh only saved history. Never reset the draft, selected chat,
        // peer agreement form, pending request or workspace controls.
        if (flows.length) await refreshHistory(id, () => generation === epoch && getSession()?.id === id && canRefresh());
        if (generation === epoch) fingerprint = next;
      } catch {
        if (generation === epoch && getSession()?.id === id) {
          region.hidden = false; region.textContent = 'Saved work status is temporarily unavailable.';
        }
      } finally { pending = false; }
    }
    return {sync, reset};
  }
  if (typeof module === 'object' && module.exports) module.exports = {create};
  else root.overkillFlows = {create};
})(typeof window === 'object' ? window : globalThis);
