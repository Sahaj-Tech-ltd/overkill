'use strict';
window.overkillShell = (() => {
  const $ = id => document.getElementById(id);
  let session = null, generation = 0, refreshing = false, outputPending = false;
  const panels = {changes: 'surface-view-changes', files: 'surface-view-files', team: 'agents-panel', browser: 'surface-view-browser'};
  function open(name) {
    for (const [key, id] of Object.entries(panels)) {
      $(id).hidden = key !== name;
      $('surface-' + key).setAttribute('aria-selected', String(key === name));
    }
    $('agents-toggle').setAttribute('aria-expanded', String(name === 'team'));
  }
  for (const name of Object.keys(panels)) $('surface-' + name).addEventListener('click', () => open(name));
  const tabs = Object.keys(panels);
  for (const [index, name] of tabs.entries()) $('surface-' + name).addEventListener('keydown', event => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault(); const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
    open(tabs[next]); $('surface-' + tabs[next]).focus();
  });
  function terminal(show) { $('terminal-dock').hidden = !show; $('terminal-toggle').setAttribute('aria-expanded', String(show)); window.overkillTerminals?.visible(show); }
  $('terminal-toggle').addEventListener('click', () => terminal($('terminal-dock').hidden));
  $('terminal-close').addEventListener('click', () => terminal(false));
  function rows(target, items, changed) {
    const node = $(target); node.replaceChildren();
    for (const item of items.slice(0, 500)) {
      const row = document.createElement('div'); row.className = 'workspace-file';
      if (changed) { const badge = document.createElement('span'); badge.className = 'file-status'; badge.textContent = item.status.trim(); row.append(badge); }
      const path = document.createElement('span'); path.textContent = changed ? item.path : item; row.append(path); node.append(row);
    }
    if (items.length > 500) { const note = document.createElement('p'); note.className = 'muted'; note.textContent = `Showing 500 of ${items.length} files.`; node.append(note); }
  }
  async function refresh() {
    if (!session || !window.overkill.workspaceStatus || refreshing) return;
    const epoch = generation; refreshing = true; $('changes-refresh').disabled = true;
    try {
      const state = await window.overkill.workspaceStatus(session.id);
      if (epoch !== generation) return;
      rows('changed-files', state.changes || [], true); rows('project-files', state.files || [], false);
      $('changes-status').textContent = state.available ? (state.changes.length ? `${state.changes.length} changed ${state.changes.length === 1 ? 'file' : 'files'} · saved on disk` : 'Your working tree is clean.') : state.message;
      $('git-status').textContent = state.available ? `${state.branch} · ${state.changes.length} changed ${state.changes.length === 1 ? 'file' : 'files'}` : 'Git unavailable';
    } catch { if (epoch === generation) { $('changes-status').textContent = 'Could not refresh workspace files. Try again.'; $('git-status').textContent = 'Workspace status unavailable'; } }
    finally { refreshing = false; $('changes-refresh').disabled = false; if (epoch !== generation) void refresh(); }
  }
  $('changes-refresh').addEventListener('click', refresh);
  setInterval(() => { if (!document.hidden) void refresh(); }, 10000);
  return {
    open, refresh,
    select(value) {
      session = value; generation++; outputPending = false; window.overkillTerminals?.select(value);
      rows('changed-files', [], true); rows('project-files', [], false);
      $('changes-status').textContent = 'Loading workspace…'; $('git-status').textContent = 'Loading workspace…';
      $('terminal-output').textContent = 'Shell output will appear when this chat runs a command.';
      void refresh();
    },
    peers(peers, self, label) {
      const container = $('sidebar-agents'); container.replaceChildren();
      for (const peer of peers) {
        const button = document.createElement('button'); button.type = 'button'; button.className = 'sidebar-agent';
        const name = document.createElement('strong'); name.textContent = peer.name + (peer.id === self?.id ? ' · this chat' : '');
        const role = document.createElement('span'); role.textContent = peer.role;
        const status = document.createElement('span'); status.className = 'muted'; status.dataset.presencePeer = peer.id; status.textContent = label(peer.presence);
        button.append(name, role, status); button.addEventListener('click', () => { open('team'); if (peer.id !== self?.id) $('peer-recipient').value = peer.name; }); container.append(button);
      }
      if (!peers.length) { const empty = document.createElement('p'); empty.className = 'muted'; empty.textContent = 'No registered agents yet. Open Team to add this chat.'; container.append(empty); }
    },
    finish() { if (outputPending) { $('terminal-output').textContent = $('terminal-output').textContent.replace(' · Running\n', ' · No result received\n'); outputPending = false; } },
    tool(data) {
      if (!/shell|terminal|exec|pty/i.test(data.name || '')) return;
      outputPending = data.output === undefined;
      let value = data.output === undefined ? data.input : data.output;
      if (data.output !== undefined) {
        try {
          const result = typeof value === 'string' ? JSON.parse(value) : value;
          if (result && ('stdout' in result || 'stderr' in result)) {
            value = [result.stdout, result.stderr, result.timed_out ? 'Command timed out.' : '', result.exit_code !== undefined ? `Exit code: ${result.exit_code}` : ''].filter(part => part !== '' && part !== undefined).join('\n');
          } else if (result?.error) value = String(result.error);
        } catch {}
      }
      if (typeof value !== 'string') value = JSON.stringify(value || {}, null, 2);
      $('terminal-output').textContent = `${data.name} · ${data.output === undefined ? 'Running' : 'Result'}\n\n${value.slice(-64000)}`;
    }
  };
})();
