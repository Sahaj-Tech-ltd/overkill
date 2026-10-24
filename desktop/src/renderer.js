'use strict';
const api = window.overkill;
const $ = id => document.getElementById(id);
let current = null, running = null, ready = false, loading = false, assistant = null;
let initialization = Promise.resolve();
let evolutionBusy=false;
const evolutionView=window.overkillEvolution.create({api,document,getSession:()=>current,canOperate:()=>ready&&!running&&!loading,onBusyChange:value=>{evolutionBusy=value;controls();}});
const canvasPanel=window.createCanvasPanel({api,getSession:()=>current,isReady:()=>ready,document});
const clarification=window.overkillClarification.create({document,api,getSession:()=>current,isReady:()=>ready,onAnswered:cancel=>{$('activity').textContent=cancel?'Clarification cancelled.':'Answer sent. Continuing…';}});
const flowView = window.overkillFlows.create({api,document,getSession:()=>current,
  canRefresh:()=>ready&&!running&&!loading&&!document.hidden,
  refreshHistory:async(id,stillCurrent)=>{const history=await api.messages(id);if(stillCurrent()){renderSavedHistory(history);scroll();}}
});
let stoppingAll=false;
let pendingSend=null;
function admitSend(request){
  if(request.accepted)return;request.accepted=true;
  if(current?.id!==request.sessionID)return;
  message('user',request.content);
  if($('message').value===request.draft)$('message').value='';
  $('activity').textContent='Thinking…';controls();scroll();
}
const sessionEnvelopes=new Map();
const chatTitle=session=>window.overkillHistory.sessionTitle(session.title||session.name,sessionEnvelopes.get(session.id));
const activeTools = new Map();
const readableError = message => String(message || '').replace(/^Error invoking remote method '[^']+': (?:Error: )?/, '');
let backendFailure = '';
function error(message = '') { if(!ready&&backendFailure)message=backendFailure; $('error').textContent = readableError(message); $('error').hidden = !message; }
function controls() {
  evolutionView.controls();
  $('message').disabled = !ready || !current || !!running || loading || evolutionBusy;
  $('send').disabled = $('message').disabled;
  $('stop').hidden = !running || (pendingSend?.id===running&&!pendingSend.accepted);
  $('emergency-stop').disabled=!ready||stoppingAll;
  $('new-session').disabled = !ready || !!running || loading || evolutionBusy;
  $('refresh').disabled = !ready || !!running || loading || evolutionBusy;
  $('settings-button').disabled = !!running || evolutionBusy;
  for(const button of $('sessions').querySelectorAll('button'))button.disabled=!!running || loading || evolutionBusy;
  agentControls();
}
function scroll() { $('transcript').scrollTop = $('transcript').scrollHeight; }
function message(role, content, {saved=false}={}) {
  const envelope=saved&&role==='user'?window.overkillHistory.parse(content):null;
  if(envelope){
    const sessionID=current?.id;
    const rendered=window.overkillHistory.render(document,envelope,{inspect:id=>{if(current?.id===sessionID)workspaceView.openTask({id});}});
    $('transcript').append(rendered.section);return rendered.body;
  }
  const section = document.createElement('article'); section.className = `message ${role === 'user' ? 'user' : 'assistant'}`;
  const label = document.createElement('h3'); label.textContent = role === 'user' ? 'You' : 'Overkill';
  const text = document.createElement('pre'); text.textContent = content;
  section.append(label, text); $('transcript').append(section); return text;
}
async function refresh() {
  const result = await api.listSessions(); $('sessions').replaceChildren();
  for (const session of result.sessions || []) {
    if (current?.id === session.id) { current = session; $('chat-title').textContent = chatTitle(session); }
    const button = document.createElement('button'); button.textContent = chatTitle(session); button.title = `${button.textContent}\n${session.folder}`;
    button.dataset.sessionId = session.id; button.disabled=!!running || loading || evolutionBusy;
    button.setAttribute('aria-current', String(current?.id === session.id));
    button.addEventListener('click', () => select(session).catch(e => error(e.message))); $('sessions').append(button);
  }
}
function renderSavedHistory(history) {
  $('transcript').replaceChildren();
    for (const item of history.messages || []) {
      const role = item.role ?? item.Role, content = item.content ?? item.Content;
      if (['user', 'assistant'].includes(role) && content) message(role, content, {saved:true});
      if (role === 'tool' && content) {
        const tool = document.createElement('details'); tool.className = 'tool';
        const summary = document.createElement('summary'); summary.textContent = `${item.name || item.Name || 'Tool'} result`;
        let readable = content;
        try { const parsed = JSON.parse(content); readable = parsed.output ?? parsed.error ?? JSON.stringify(parsed, null, 2); } catch {}
        const output = document.createElement('pre'); output.textContent = typeof readable === 'string' ? readable : JSON.stringify(readable, null, 2);
        const raw = document.createElement('details');
        const rawSummary = document.createElement('summary'); rawSummary.textContent = 'Raw result';
        const rawText = document.createElement('pre'); rawText.textContent = content;
        raw.append(rawSummary, rawText); tool.append(summary, output, raw); $('transcript').append(tool);
      }
    }
}
async function select(session) {
  if (running || loading) return;
  loading = true; controls(); error();
  try {
    const history = await api.messages(session.id);
    sessionEnvelopes.set(session.id,(history.messages||[]).filter(item=>(item.role??item.Role)==='user').map(item=>window.overkillHistory.parse(item.content??item.Content)).find(Boolean));
    current = session; evolutionView.reset(); flowView.reset(); clarification.reset();void clarification.sync(); canvasPanel.select(); $('chat-title').textContent = chatTitle(session);
    $('workspace-path').textContent = session.folder; $('transcript').replaceChildren();
    window.overkillShell?.select(session);window.overkillWorkers?.select(session);
    renderSavedHistory(history);
    workspaceView.reset(); agreementPicker.reset(); $('peer-contract-pins').value = ''; clearReusedRequest(); reuseGeneration++;
    agentSelf = null; agentPeers = []; peerPage = null; peerDraft = null; browsingOlder = false;
    agentError(); agentError('', 'refresh');
    $('agent-name').value = ''; $('agent-role').value = ''; $('agent-repository-scope').checked = false;
    renderPeers(); renderCorrespondence([]);
    await refresh(); await refreshAgents(); scroll();
  } finally { loading = false; controls(); }
}
function applyState(state) {
  const wasReady=ready; ready = state.ready; window.overkillTerminals?.connection(ready);window.overkillWorkers?.connection(ready);
  if(ready||state.starting||state.stopped)backendFailure='';else if(state.error)backendFailure=state.error;
  $('settings-stop-warning').hidden=!state.stopped;$('settings-save').textContent=state.stopped?'Reconnect and allow queued work':'Save and connect';
  if(wasReady!==ready){canvasPanel.connection();if(!ready)clarification.reset();else void clarification.sync();}
  $('connection').textContent = ready ? `${state.settings.provider} · ${state.settings.model} · Local database` : state.starting ? 'Connecting to the local backend…' : 'Connect a provider to begin.';
  if (state.workspace) $('workspace-path').textContent = state.workspace;
  if (state.error) error(state.error); controls();
}
async function showSettings() {
  // Startup state can arrive after the user has started editing this dialog.
  if ($('settings').open) return;
  const state = await api.state();
  if ($('settings').open) return;
  $('settings-stop-warning').hidden=!state.stopped;$('settings-save').textContent=state.stopped?'Reconnect and allow queued work':'Save and connect';
  $('provider').value = state.settings?.provider || 'openai'; $('model').value = state.settings?.model || '';
  $('base-url').value = state.settings?.baseURL || ''; $('api-key').value = '';
  $('workspace-instructions').checked=state.settings?.workspaceInstructions===true; $('autonomous-mode').checked=state.settings?.autoMode===true;
  $('api-key').placeholder = state.settings?.hasKey ? 'Leave blank to keep your saved key' : 'Enter API key';
  $('settings-error').textContent = ''; if (!$('settings').open) $('settings').showModal();
}
$('settings-button').addEventListener('click', () => showSettings().catch(e => error(e.message)));
$('settings-cancel').addEventListener('click', () => { $('api-key').value = ''; $('settings').close(); });
$('settings').addEventListener('close', () => { $('api-key').value = ''; });
$('settings-form').addEventListener('submit', async event => {
  event.preventDefault(); $('settings-save').disabled = true; $('settings-error').textContent = 'Starting the local database and backend…';
  const input = {provider: $('provider').value, model: $('model').value.trim(), baseURL: $('base-url').value.trim(), apiKey: $('api-key').value, workspaceInstructions:$('workspace-instructions').checked, autoMode:$('autonomous-mode').checked}; $('api-key').value = '';
  try { applyState(await api.saveSettings(input)); $('emergency-stop-status').hidden=true;$('emergency-stop-status').textContent=''; $('settings').close(); error(); await refresh(); }
  catch (e) { $('settings-error').textContent = readableError(e.message); }
  finally { input.apiKey = ''; $('settings-save').disabled = false; }
});
$('workspace').addEventListener('click', async () => {
  try { const folder = await api.pickWorkspace(); if (folder) $('workspace-path').textContent = folder; } catch (e) { error(e.message); }
});
$('new-session').addEventListener('click', async () => {
  if (running || loading) return;
  try { error(); const result = await api.createSession(); await select(result.session); } catch (e) { error(e.message); }
});
$('refresh').addEventListener('click', () => (current ? select(current) : refresh()).catch(e => error(e.message)));
$('stop').addEventListener('click', async () => {
  const responseID = running, sessionID = current?.id;
  if (!responseID || !sessionID) return;
  try { await api.stop(sessionID); if (running === responseID) $('activity').textContent = 'Stopping…'; }
  catch (e) { if (current?.id === sessionID) error(e.message); }
});
$('composer').addEventListener('submit', async event => {
  event.preventDefault(); if (running || !current || !ready) return;
  const content = $('message').value.trim(); if (!content) return;
  error(); running = crypto.randomUUID(); assistant = null; activeTools.clear();
  const request={id:running,sessionID:current.id,content,draft:$('message').value,accepted:false};pendingSend=request;controls();
  $('activity').textContent='Waiting for backend admission…';
  try {
    const result=await api.send({id:request.id,sessionID:request.sessionID,message:content});
    if(result?.accepted!==true||result.id!==request.id)throw new Error('Backend admission was not confirmed.');
    // The admitted event precedes stream events; this fallback is idempotent.
    admitSend(request);
  } catch (e) {
    if(!request.accepted&&pendingSend===request&&current?.id===request.sessionID){error(e.message);if(running===request.id){running=null;$('activity').textContent='Admission not confirmed. Draft preserved; review the chat before submitting again.';}controls();}
  } finally {if(pendingSend===request)pendingSend=null;}
});
$('message').addEventListener('keydown', event => { if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) { event.preventDefault(); $('composer').requestSubmit(); } });
api.onEvent(event => {
  if (event.type === 'backend_state') { initialize(event.state).catch(e => error(e.message)); return; }
  if (event.type === 'backend_exit') { backendFailure=event.message;window.overkillShell?.finish(); clarification.reset();ready = false; window.overkillTerminals?.connection(false);window.overkillWorkers?.connection(false); canvasPanel.connection(); running = null; $('connection').textContent='Backend stopped · reconnect in Provider settings';controls(); error(event.message); return; }
  if(event.type==='admitted'){if(pendingSend?.id===event.id&&pendingSend.sessionID===event.data?.sessionID)admitSend(pendingSend);return;}
  if (!running || event.id !== running) return;
  const data = event.data || {};
  if (event.type === 'text') { if (!assistant) assistant = message('assistant', ''); assistant.textContent += data.content || ''; scroll(); }
  if (event.type === 'tool_call') {
    window.overkillShell?.tool(data);
    const name = data.name || 'Tool', pending = activeTools.get(name) || [];
    if (data.phase === 'output' || data.output !== undefined) {
      let tool = pending.shift();
      if (!tool) { tool = document.createElement('details'); tool.className = 'tool'; tool.append(document.createElement('summary'), document.createElement('pre')); $('transcript').append(tool); }
      let output = data.output; try { const parsed = JSON.parse(output); output = parsed.output || parsed.error || JSON.stringify(parsed, null, 2); } catch {}
      tool.firstChild.textContent = `${name} · Result`; tool.lastChild.textContent = typeof output === 'string' ? output : JSON.stringify(output, null, 2);
    } else {
      const tool = document.createElement('details'); tool.className = 'tool';
      const summary = document.createElement('summary'); summary.textContent = `${name} · Running…`;
      const details = document.createElement('pre'); details.textContent = typeof data.input === 'string' ? data.input : JSON.stringify(data.input || {}, null, 2);
      tool.append(summary, details); $('transcript').append(tool); pending.push(tool);
    }
    activeTools.set(name, pending); scroll();
  }
  if (event.type === 'status') $('activity').textContent = data.content || data.phase || 'Working…';
  if (event.type === 'done' || event.type === 'error') {
    window.overkillShell?.finish();
    if (event.type === 'error') error(data.message || data.error || 'The response failed.');
    else { if (typeof data.response === 'string') { if (assistant) assistant.textContent = data.response; else if (data.response) message('assistant', data.response); } if (data.blocked) error(data.block_reason || 'Agent stopped at a safety limit.'); }
    for (const [name, pending] of activeTools) for (const tool of pending) tool.firstChild.textContent = `${name} · No result received`;
    activeTools.clear(); running = null; assistant = null; $('activity').textContent = 'Ready when you are.'; controls();
    refresh().catch(e => error(e.message)); canvasPanel.refresh(); scroll();
  }
});
function initialize(state) {
  initialization = initialization.catch(() => {}).then(async () => { applyState(state); if (ready) await refresh(); else if (!state.starting) await showSettings(); });
  return initialization;
}
api.state().then(initialize).catch(e => error(e.message));

let agentSelf = null, agentPeers = [], peerPage = null, agentRefreshing = false, agentSending = false, peerDraft = null, browsingOlder = false, refreshPending = false;
const agentErrors = {action: '', refresh: ''};
let reusedRequest = null, reuseGeneration = 0;
function clearReusedRequest() {
  reusedRequest = null; $('peer-reuse-note').hidden = true; $('peer-reuse-clear').hidden = true; agentControls();
}
for (const id of ['peer-recipient', 'peer-message', 'peer-task-name', 'peer-token-limit', 'peer-dependencies', 'peer-contract-pins']) {
  $(id).addEventListener('input', () => {
    reuseGeneration++;
    if (reusedRequest && id === 'peer-task-name' && $('peer-task-name').value.trim() !== reusedRequest.taskID) clearReusedRequest();
  });
}
$('peer-reuse-clear').addEventListener('click', () => { reuseGeneration++; clearReusedRequest(); peerDraft = null; $('peer-task-name').value = ''; $('peer-dependencies').value = ''; $('peer-contract-pins').value = ''; $('agents-status').textContent = 'New request. Review the task name and token limit before sending.'; });
function agentError(value = '', source = 'action') {
  agentErrors[source] = readableError(value);
  const message = Object.values(agentErrors).filter(Boolean).join(' ');
  $('agents-error').textContent = message; $('agents-error').hidden = !message;
}
function agentControls() {
  const available = ready && !!current && !loading;
  $('agents-refresh').disabled = !available || agentRefreshing;
  $('agent-register').disabled = !available || !!running || !!agentSelf;
  $('agent-name').disabled = !available || !!agentSelf;
  $('agent-role').disabled = !available || !!agentSelf;
  $('agent-repository-scope').disabled = !available || !!agentSelf;
  $('peer-send').disabled = !available || !agentSelf || agentSending || !agentPeers.some(peer => peer.id !== agentSelf.id && peer.enabled);
  $('peer-recipient').disabled = $('peer-send').disabled;
  $('peer-message').disabled = $('peer-send').disabled;
  $('peer-task-name').disabled = $('peer-send').disabled;
  $('peer-choose-agreements').disabled = $('peer-send').disabled || !!reusedRequest;
  $('peer-dependencies').disabled = $('peer-send').disabled || !!reusedRequest;
  $('peer-token-limit').disabled = $('peer-send').disabled || !!reusedRequest;
}
function renderPeers() {
  window.overkillShell?.peers(agentPeers, agentSelf, value => window.overkillWorkspace.presenceLabel(ready ? value : null));
  const peerNames=new Map(agentPeers.map(peer=>[peer.id,peer.name]));
  for(const card of $('transcript').querySelectorAll('[data-coordination-id]')){
    card.querySelector('.coordination-route').textContent=window.overkillHistory.route({from:card.dataset.peerFrom,to:card.dataset.peerTo},peerNames);
  }
  $('agent-registration').hidden = !!agentSelf;
  $('agent-identity').hidden = !agentSelf;
  $('agent-identity').textContent = agentSelf ? `${agentSelf.name} · ${agentSelf.role} · ${window.overkillWorkspace.peerScopeLabel(agentSelf)}` : '';
  $('peer-scope-heading').textContent = agentSelf?.project_id?.startsWith('repository:') ? 'REPOSITORY WORKTREES' : 'THIS CHECKOUT';
  $('agents-status').textContent = !current ? 'Select a chat to discover agents.' : agentSelf ? `Registered as ${agentSelf.name}. Registration and project scope are saved with this chat.` : 'This chat is not registered. Choose a unique workspace name and role.';
  const selected = $('peer-recipient').value;
  $('agent-peers').replaceChildren(); $('peer-recipient').replaceChildren();
  for (const peer of agentPeers) {
    const card = document.createElement('div'); card.className = 'peer-card';
    const name = document.createElement('strong'); name.textContent = `${peer.name}${peer.id === agentSelf?.id ? ' · this chat' : ''}`;
    const role = document.createElement('p'); role.className = 'muted'; role.textContent = `${peer.role}${peer.enabled ? '' : ' · disabled'}`;
    const presence=document.createElement('p');presence.className='peer-presence muted';presence.dataset.presencePeer=peer.id;presence.textContent=window.overkillWorkspace.presenceLabel(ready?peer.presence:null);
    if(peer.repository) card.append(Object.assign(document.createElement('p'),{className:'muted',textContent:`${peer.repository.branch || 'Detached checkout'} · ${peer.repository.root}`}));
    card.append(name, role, presence); $('agent-peers').append(card);
    if (peer.id !== agentSelf?.id && peer.enabled) { const option = document.createElement('option'); option.value = peer.name; option.textContent = peer.name; $('peer-recipient').append(option); }
  }
  if ([...$('peer-recipient').options].some(option => option.value === selected)) $('peer-recipient').value = selected;
  if (!agentPeers.length) $('agent-peers').textContent = 'No registered agents in this workspace yet.';
  agentControls();
}
function renderCorrespondence(messages, append = false) {
  if (!append) $('peer-messages').replaceChildren();
  if (!messages.length && !append) $('peer-messages').textContent = agentSelf ? 'No correspondence yet.' : 'Register this chat to view correspondence.';
  const names = new Map(agentPeers.map(peer => [peer.id, peer.name]));
  for (const item of messages) {
    const card = document.createElement('article'); card.className = 'peer-message'; card.dataset.state = item.state;
    const heading = document.createElement('strong'); heading.textContent = `${names.get(item.from) || item.from} → ${names.get(item.to) || item.to}`;
    const state = document.createElement('div'); state.className = `peer-state state-badge ${item.state}`; state.textContent = `${workspaceView.stateLabel(item.state)} · ${item.kind}`;
    const text = document.createElement('pre');
    const response = item.payload && typeof item.payload === 'object' && typeof item.payload.response === 'string' ? item.payload.response : null;
    text.textContent = response ?? (typeof item.payload === 'string' ? item.payload : JSON.stringify(item.payload, null, 2));
    if (response !== null && item.payload.truncated) text.textContent += '\n[Delivery excerpt truncated; full response is in the recipient chat.]';
    const date = document.createElement('small'); date.className = 'muted'; date.textContent = new Date(item.updated_at).toLocaleString();
    if (item.task_id) card.append(Object.assign(document.createElement('p'), {textContent: item.task_id, className: 'task-name'}));
    card.append(heading, state, text, date);
    const inspect = document.createElement('button'); inspect.textContent = 'Inspect task'; inspect.addEventListener('click', () => workspaceView.openTask(item)); card.append(inspect);
    if (item.from === agentSelf?.id && ['failed', 'cancelled', 'blocked'].includes(item.state) && item.kind === 'request') {
      const reuse = document.createElement('button'); reuse.textContent = 'Review and resend';
      reuse.addEventListener('click', async () => {
        const sessionID=current.id, generation=++reuseGeneration; reuse.disabled=true;
        try {
          const result=await api.taskUsage({sessionID,messageID:item.id}); if(current?.id!==sessionID || reuseGeneration!==generation)return;
          $('peer-recipient').value = names.get(item.to) || ''; $('peer-message').value = typeof item.payload === 'string' ? item.payload : JSON.stringify(item.payload, null, 2);
          $('peer-task-name').value = item.task_id || ''; $('peer-token-limit').value = result.usage?.limits?.tokens || '';
          $('peer-dependencies').value = (item.depends_on || []).join(', ');
          $('peer-contract-pins').value = window.overkillRequestAgreements.formatPins(item.required_contracts);
          reusedRequest = {requiredContracts: structuredClone(item.required_contracts || []), dependsOn: [...(item.depends_on || [])], sessionID, conversationID: item.conversation_id, taskID: item.task_id || '', costLimit: result.usage?.limits?.cost_usd || 0};
          $('peer-reuse-note').textContent = `Reusing the original task, prerequisites, agreements and remaining budget.${reusedRequest.costLimit ? ` Cost limit: $${reusedRequest.costLimit}.` : ''}`;
          $('peer-reuse-note').hidden = false; $('peer-reuse-clear').hidden = false; agentControls();
          $('peer-message').focus(); $('agents-status').textContent = 'Review before sending. The existing budget is preserved; previous work may have changed files. A blocked recipient waits for review or cancellation.';
        } catch(e) {if(current?.id===sessionID && reuseGeneration===generation)agentError(e.message);} finally {reuse.disabled=false;}
      });
      card.append(reuse);
    }
    if (item.from === agentSelf?.id && ['accepted', 'started', 'blocked'].includes(item.state)) {
      const cancel = document.createElement('button'); cancel.textContent = 'Cancel request';
      const sessionID = current.id;
      cancel.addEventListener('click', async () => {
        cancel.disabled = true;
        try { await api.cancelPeer({sessionID, messageID: item.id, revision: item.revision}); if (current?.id === sessionID) agentError(); await refreshAgents(); }
        catch (e) { if (current?.id === sessionID) { agentError(e.message); cancel.disabled = false; } }
      });
      card.append(cancel);
    }
    $('peer-messages').append(card);
  }
}
async function refreshAgents(older = false) {
  if (!ready || !current || (older && !peerPage?.has_more)) return;
  if (agentRefreshing) { refreshPending = true; return; }
  const sessionID = current.id;
  browsingOlder = older;
  agentRefreshing = true; agentControls();
  try {
    const peers = await api.peers({sessionID});
    if (current?.id !== sessionID) return;
    agentSelf = peers.self; agentPeers = peers.peers || []; renderPeers();
    if (agentSelf) {
      const page = await api.correspondence({sessionID, ...(older ? {beforeID: peerPage.before_id} : {})});
      if (current?.id !== sessionID) return;
      peerPage = page; renderCorrespondence(page.messages || [], older); $('peer-older').hidden = !page.has_more;
    } else { peerPage = null; renderCorrespondence([]); $('peer-older').hidden = true; }
    agentError('', 'refresh');
    await workspaceView.refresh({registered: !!agentSelf});
  } catch (e) { if (current?.id === sessionID) agentError(e.message, 'refresh'); }
  finally {
    agentRefreshing = false; agentControls();
    if (refreshPending || current?.id !== sessionID) { refreshPending = false; queueMicrotask(() => refreshAgents()); }
  }
}
$('agents-toggle').addEventListener('click', () => { window.overkillShell?.open($('agents-panel').hidden ? 'team' : 'changes'); if (!$('agents-panel').hidden) refreshAgents(); });
$('agents-refresh').addEventListener('click', () => { refreshAgents(); workspaceView.refreshReviews(); });
$('peer-older').addEventListener('click', () => refreshAgents(true));
$('agent-registration').addEventListener('submit', async event => {
  event.preventDefault(); if (!current) return;
  const sessionID = current.id; $('agent-register').disabled = true;
  try { await api.registerAgent({sessionID, name: $('agent-name').value.trim(), role: $('agent-role').value.trim(), repositoryScope: $('agent-repository-scope').checked}); if (current?.id === sessionID) agentError(); await refreshAgents(); }
  catch (e) { if (current?.id === sessionID) agentError(e.message); } finally { agentControls(); }
});
$('peer-composer').addEventListener('submit', async event => {
  event.preventDefault(); if (!current || !agentSelf || agentSending) return;
  const input = {sessionID: current.id, to: $('peer-recipient').value, message: $('peer-message').value.trim()};
  if (!input.message || !input.to) return;
  const dependencies = $('peer-dependencies').value.trim().split(/[\s,]+/).filter(Boolean);
  if (dependencies.length) input.dependsOn = dependencies;
  let pins;
  try {pins = window.overkillRequestAgreements.parsePins($('peer-contract-pins').value);} catch(e) {agentError(e.message);return;}
  if (pins.length) input.requiredContracts = pins;
  if ($('peer-task-name').value.trim()) input.taskID = $('peer-task-name').value.trim();
  if ($('peer-token-limit').value !== '') input.tokenLimit = Number($('peer-token-limit').value);
  if (reusedRequest?.sessionID === current.id && (input.taskID || '') === reusedRequest.taskID) {
    input.requiredContracts = structuredClone(reusedRequest.requiredContracts); input.dependsOn = [...reusedRequest.dependsOn]; input.conversationID = reusedRequest.conversationID; input.costLimit = reusedRequest.costLimit;
    input.tokenLimit ??= 0;
  }
  const signature = JSON.stringify(input);
  if (peerDraft?.signature !== signature) peerDraft = {signature, key: crypto.randomUUID()};
  const draft = peerDraft;
  agentSending = true; agentControls();
  try {
    const result = await api.sendPeer({...input, key: draft.key});
    if (current?.id === input.sessionID) { agentError(); $('peer-message').value = ''; $('agents-status').textContent = `Request ${result.message.state}. Refresh to follow delivery.`; }
    if (peerDraft === draft) peerDraft = null;
    if (current?.id === input.sessionID) await refreshAgents();
  } catch (e) { if (current?.id === input.sessionID) agentError(e.message); } finally { agentSending = false; agentControls(); }
});
const agreementPicker = window.overkillRequestAgreements.create({api,document,session:()=>current,getPins:()=>$('peer-contract-pins').value,onApply:pins=>{$('peer-contract-pins').value=window.overkillRequestAgreements.formatPins(pins);reuseGeneration++;}});
$('peer-choose-agreements').addEventListener('click',()=>agreementPicker.open());
const workspaceView = window.overkillWorkspace.create({api, document, session: () => current, peers: () => agentPeers});
setInterval(() => { if (ready && current && !document.hidden && !browsingOlder) refreshAgents(); }, 3000);

// Update only presence leaves: expiry must remain visible after transport loss,
// without rebuilding peer controls or discarding a request draft.
setInterval(()=>{
  const states=new Map(agentPeers.map(peer=>[peer.id,window.overkillWorkspace.presenceLabel(ready?peer.presence:null)]));
  for(const node of document.querySelectorAll('[data-presence-peer]'))node.textContent=states.get(node.dataset.presencePeer)||'Presence unknown';
},1000);

setInterval(()=>{void clarification.sync();},500);
$('emergency-stop').addEventListener('click',async()=>{if(!ready||stoppingAll)return;stoppingAll=true;controls();$('emergency-stop-status').hidden=false;$('emergency-stop-status').textContent='Stopping agents owned by this app… Earlier effects cannot be undone.';try{const result=await api.emergencyStop();if(result.status!=='stopped'||result.reconnectRequired!==true)throw new Error('Stop outcome is unconfirmed.');clarification.reset();applyState(await api.state());$('emergency-stop-status').textContent='All agents in this app are stopped. Reconnect in Provider settings to resume; queued requests may resume. Earlier effects remain. External agents are unaffected.';}catch(e){$('emergency-stop-status').textContent='Stop request failed: '+readableError(e.message);}finally{stoppingAll=false;controls();}});

setInterval(()=>{void flowView.sync();},1000);
