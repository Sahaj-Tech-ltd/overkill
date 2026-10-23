'use strict';
(function(root) {
  const labels = Object.freeze({accepted: 'Queued', started: 'Working', blocked: 'Needs review', completed: 'Completed', failed: 'Failed', cancelled: 'Cancelled', passed: 'Passed', running: 'Running', skipped: 'Skipped', canceled: 'Cancelled', unconfirmed: 'Unconfirmed'});
  const stateLabel = state => labels[state] || 'Unknown';
  function presenceLabel(presence, now=Date.now()) {
    if(presence?.source!=='runtime_lease'||!Number.isSafeInteger(presence.observers)||presence.observers<1||!Number.isFinite(Date.parse(presence.expires_at))||Date.parse(presence.expires_at)<=now)return 'Presence unknown';
    return ({ready:'Runtime idle',working:'Working',waiting_for_input:'Waiting for input',busy:'Finishing an operation',multiple_runtimes:'Multiple runtimes observed'})[presence.state]||'Presence unknown';
  }
  const peerScopeLabel = peer => peer?.project_id?.startsWith('repository:') ? 'Repository worktrees' : 'This checkout';
  const readable = value => typeof value === 'string' ? value : JSON.stringify(value ?? '', null, 2);
  function usageSummary(usage) {
    const tokens = (usage.input_tokens || 0) + (usage.output_tokens || 0);
    const uncertain = !!(usage.unknown_usage || usage.unconfirmed_executions || usage.pending_executions);
    return {tokens, uncertain, tokenText: `${tokens.toLocaleString()} reported${usage.limits?.tokens ? ` / ${usage.limits.tokens.toLocaleString()} limit` : ''}`, costText: usage.unknown_price || uncertain ? 'Cost estimate incomplete' : `$${Number(usage.estimated_cost_usd || 0).toFixed(6)} estimated`, review: !!usage.requires_review};
  }
  function blockerText(blocker, names = new Map()) {
    if (blocker.reason === 'owner_disabled') return 'The contract owner is disabled.';
    if (String(blocker.reason).startsWith('current_revision_not_acknowledged_by:')) {
      const peer = String(blocker.reason).split(':')[1];
      return `Awaiting acknowledgement${blocker.expected_revision ? ` of revision ${blocker.expected_revision}` : ''} from ${names.get(peer) || 'a project peer'}.`;
    }
    if (blocker.reason === 'required_contract_deleted') return 'Pinned agreement is unavailable. Review before creating a new request.';
    if (blocker.reason === 'required_contract_revision_changed') return `Pinned revision ${blocker.expected_revision ?? 'unknown'} changed to ${blocker.actual_revision ?? 'unknown'}. Review before creating a new request.`;
    if (blocker.reason === 'dependency_revision_changed') return `Dependency changed: expected revision ${blocker.expected_revision ?? 'unknown'}, current revision ${blocker.actual_revision ?? 'unknown'}.`;
    return String(blocker.reason || 'Agreement requires review').replaceAll('_', ' ');
  }
  function create({api, document: doc, session, peers = () => []}) {
    const $ = id => doc.getElementById(id);
    const el = (tag, text, className) => { const node = doc.createElement(tag); if (text !== undefined) node.textContent = text; if (className) node.className = className; return node; };
    const note = (container, text) => container.append(el('p', text, 'muted'));
    const row = (container, name, value) => { const item = el('div', undefined, 'detail-row'); item.append(el('span', name, 'muted'), el('span', value)); container.append(item); };
    const disclosure = (container, label, value) => { const details = el('details'); details.append(el('summary', label), el('pre', readable(value))); container.append(details); };
    const fail = (container, error) => { container.replaceChildren(el('p', String(error?.message || error), 'panel-error')); };
    const unwrap = response => response?.result ?? response;
    let taskGeneration = 0, reviewExpanded = false, contractLoadGeneration = 0, contractsFingerprint = null;
    const contractStates = new Map(), contractViews = new Map();
    let contractList = [], contractsLoaded = false, editor, requestReview, usageReconciliation, selectedUsage;
    function editContract(contract) {
      if (!registered || !session()?.id || !contractsLoaded) return;
      const owner=peers().find(peer=>peer.session_id===session()?.id);
      if(contract && contract.owner_peer_id!==owner?.id)return;
      editor ||= (typeof module==='object'&&module.exports ? require('./contract-editor.js') : root.overkillContractEditor).create({document:doc,api,session,onSaved:()=>loadContracts().catch(error=>fail($('workspace-contracts'),error))});
      editor.open(contract,contractList);
    }
    let epoch = 0, selected = null, events = [], revision = 0, moreEvents = false, reviews = [], reviewCursor = 0, moreReviews = false, registered = false, activeTab = 'tasks', busy = false, pending = false;
    function valid(id, generation) { return session()?.id === id && epoch === generation; }
    function reset() {
      editor?.reset(); requestReview?.reset(); usageReconciliation?.reset(); $('task-reconcile').hidden=true; selectedUsage=null; $('task-review').hidden=true; contractList=[]; contractsLoaded=false; $('contract-new').disabled=true; epoch++; taskGeneration++; contractLoadGeneration++; contractsFingerprint = null; contractStates.clear(); contractViews.clear(); reviewExpanded = false; selected = null; events = []; reviews = []; revision = 0; reviewCursor = 0; moreReviews = false; registered = false;
      if ($('task-inspector').open) $('task-inspector').close();
      for (const id of ['recorded-reviews', 'workspace-contracts', 'workspace-checks']) $(id).replaceChildren();
      $('reviews-more').hidden = true;
      $('task-error').textContent = '';
    }
    function tab(name) {
      activeTab = name;
      for (const key of ['tasks', 'contracts', 'checks']) {
        $(`team-tab-${key}`).setAttribute('aria-selected', String(key === name));
        $(`team-view-${key}`).hidden = key !== name;
      }
      return refresh({registered});
    }
    function renderReviews() {
      const container = $('recorded-reviews'); container.replaceChildren();
      if (!reviews.length) note(container, registered ? 'No recorded blockers in this page.' : 'Register this chat to view peer blockers.');
      for (const review of reviews) {
        const card = el('article', undefined, 'review-card'); card.dataset.messageId=review.parent_message_id||''; card.append(el('strong', String(review.reason || 'Review required').replaceAll('_', ' ')), el('p', 'Recorded review trigger · inspect task for current state', 'muted'));
        if (review.created_at) card.append(el('small', new Date(review.created_at).toLocaleString(), 'muted'));
        if (review.parent_message_id) { const inspect = el('button', 'Inspect task'); inspect.addEventListener('click', () => openTask({id: review.parent_message_id})); card.append(inspect); }
        container.append(card);
      }
      $('reviews-more').hidden = !moreReviews;
    }
    async function loadReviews(append = false) {
      const id = session()?.id, generation = epoch; if (!id || !registered) return;
      if (append) reviewExpanded = true;
      const response = await api.escalations({sessionID: id, after: append ? reviewCursor : 0}); if (!valid(id, generation)) return;
      reviews = [...new Map([...(append ? reviews : []), ...(response.escalations || [])].map(item => [item.sequence, item])).values()];
      reviewCursor = response.next_cursor; moreReviews = response.has_more; renderReviews();
    }
    function currentContract(view) {
      return registered && valid(view.sessionID, view.epoch) && contractStates.get(view.contract.id) === view.state && contractViews.get(view.contract.id) === view;
    }
    function renderAcknowledgement(view) {
      const {state, contract, button, status} = view;
      button.disabled = state.pending || state.acknowledged || state.stale;
      button.textContent = state.pending ? 'Recording acknowledgement…' : state.acknowledged ? `Revision ${contract.revision} acknowledged` : `Acknowledge revision ${contract.revision}`;
      status.textContent = state.error || (state.acknowledged ? 'Your acknowledgement is saved for this chat.' : 'Records agreement for this chat; does not mark implementation or checks complete.');
      status.className = state.error ? 'panel-error' : 'muted';
    }
    async function loadContractReadiness(view) {
      if (!currentContract(view) || view.state.pending) return;
      const {state, contract, readiness} = view, request = ++state.readinessGeneration;
      if (!state.loaded) readiness.replaceChildren(el('p', 'Checking agreement…', 'muted'));
      try {
        const result = unwrap(await api.contractReadiness({sessionID: view.sessionID, contractID: contract.id}));
        if (!currentContract(view) || state.readinessGeneration !== request) return;
        if (result.contract_id !== contract.id || result.revision !== contract.revision || !result.peer_id || !Array.isArray(result.blockers) || result.ready !== (result.blockers.length === 0) || !result.blockers.every(blocker => typeof blocker.contract_id === 'string' && typeof blocker.reason === 'string')) {
          state.stale = true; readiness.replaceChildren(el('p', 'Agreement changed. Refresh agreements to inspect the current revision.', 'status-review'));
        } else {
          state.stale = false; state.loaded = true;
          state.acknowledged = !result.blockers.some(blocker => blocker.contract_id === contract.id && blocker.expected_revision === contract.revision && blocker.reason === `current_revision_not_acknowledged_by:${result.peer_id}`);
          if (state.acknowledged) state.error = '';
          readiness.replaceChildren(el('p', result.ready ? 'Agreement current' : 'Agreement needs review', result.ready ? 'status-good' : 'status-review'));
          for (const blocker of result.blockers) note(readiness, blockerText(blocker, new Map(peers().map(peer => [peer.id, peer.name]))));
          note(readiness, 'Agreement readiness does not prove that acceptance checks passed.');
        }
        renderAcknowledgement(view);
      } catch (error) { if (currentContract(view) && state.readinessGeneration === request) { fail(readiness, error); state.loaded = false; } }
    }
    async function acknowledgeContract(view) {
      if (!currentContract(view) || view.state.pending || view.state.acknowledged || view.state.stale) return;
      const {state, contract} = view;
      state.pending = true; state.error = ''; state.readinessGeneration++; renderAcknowledgement(view);
      try {
        const result = unwrap(await api.acknowledgeContract({sessionID: view.sessionID, contractID: contract.id, revision: contract.revision}));
        if (!currentContract(view)) return;
        if (result.contract_id !== contract.id || result.revision !== contract.revision || !result.peer_id || !result.acknowledged_at) throw new Error('Acknowledgement could not be confirmed. Refresh agreements before retrying.');
        state.acknowledged = true;
      } catch (error) { if (currentContract(view)) state.error = String(error?.message || error); }
      finally { if (currentContract(view)) { state.pending = false; renderAcknowledgement(view); } }
      if (currentContract(view) && state.acknowledged) await loadContractReadiness(view);
    }
    async function loadContracts() {
      const id = session()?.id, generation = epoch, request = ++contractLoadGeneration, container = $('workspace-contracts');
      if (!id || !registered) { container.replaceChildren(); contractsFingerprint = null; contractViews.clear(); contractStates.clear(); note(container, 'Register this chat to view shared contracts.'); return; }
      let contracts;
      try {contracts = unwrap(await api.contracts({sessionID: id}));} catch (error) {if(valid(id,generation) && request===contractLoadGeneration){contractsFingerprint=null;contractViews.clear();contractsLoaded=false;$('contract-new').disabled=true;}throw error;}
      if (!valid(id, generation) || request !== contractLoadGeneration) return;
      if (!Array.isArray(contracts)) { contractsFingerprint=null;contractViews.clear();throw new Error('The contract list could not be read.'); }
      contractList = contracts; contractsLoaded=true; $('contract-new').disabled=false;
      const fingerprint = JSON.stringify(contracts);
      if (contractsFingerprint === fingerprint) {
        await Promise.all([...contractViews.values()].filter(view => view.detail.open).map(loadContractReadiness));
        return;
      }
      contractsFingerprint = fingerprint; contractViews.clear(); container.replaceChildren();
      const active = new Set(contracts.map(contract => contract.id));
      for (const key of contractStates.keys()) if (!active.has(key)) contractStates.delete(key);
      if (!contracts.length) { note(container, 'No shared interface contracts yet.'); return; }
      const checks = [];
      for (const contract of contracts) {
        let state = contractStates.get(contract.id);
        if (!state || state.revision !== contract.revision) {
          state = {revision: contract.revision, open: state?.open || false, pending: false, acknowledged: false, stale: false, error: '', loaded: false, readinessGeneration: 0};
          contractStates.set(contract.id, state);
        }
        const card = el('article', undefined, 'contract-card'); card.dataset.contractId = contract.id;
        card.append(el('strong', contract.spec.name), el('p', `${contract.spec.endpoint} · schema ${contract.spec.schema_version}`, 'muted'));
        const detail = el('details'); detail.open = state.open; detail.append(el('summary', `Revision ${contract.revision} · inspect agreement`));
        const content = el('div'); row(content, 'Owner', peers().find(peer => peer.id === contract.owner_peer_id)?.name || 'Unlisted project peer'); row(content, 'Workspace', contract.spec.workspace); row(content, 'Owned paths', (contract.spec.owned_paths || []).join(', '));
        for (const item of contract.spec.acceptance_criteria || []) note(content, item);
        for (const artifact of contract.spec.artifacts || []) row(content, artifact.name, artifact.path);
        for (const dependency of contract.spec.dependencies || []) row(content, 'Dependency', `${dependency.contract_id} · revision ${dependency.revision}`);
        disclosure(content, 'Response schema', contract.spec.schema);
        const readiness = el('div'), actions = el('div', undefined, 'contract-actions'), button = el('button'), status = el('p');
        button.type = 'button';
        const reload = el('button', 'Refresh agreements'); reload.type = 'button'; reload.addEventListener('click', () => loadContracts().catch(error => { if (valid(id, generation)) fail(container, error); }));
        actions.append(button, reload, status);
        const owner=peers().find(peer=>peer.session_id===id);
        if(owner?.id===contract.owner_peer_id){const edit=el('button','Edit contract');edit.type='button';edit.addEventListener('click',()=>editContract(contract));actions.append(edit);}
        const view = {contract, state, detail, readiness, button, status, sessionID: id, epoch: generation}; contractViews.set(contract.id, view);
        renderAcknowledgement(view);
        button.addEventListener('click', () => acknowledgeContract(view));
        detail.addEventListener('toggle', () => {if (!currentContract(view)) return; state.open = detail.open; if (detail.open) return loadContractReadiness(view);});
        content.append(readiness, actions); detail.append(content); card.append(detail); container.append(card);
        if (state.open) checks.push(loadContractReadiness(view));
      }
      await Promise.all(checks);
    }
    async function loadChecks() {
      const id = session()?.id, generation = epoch; if (!id) return;
      const results = await Promise.allSettled([api.verification({sessionID: id}), api.evaluation({sessionID: id})]); if (!valid(id, generation)) return;
      const container = $('workspace-checks'); container.replaceChildren(); note(container, 'Recorded checks for this chat. They are not an overall task-completion verdict.');
      if (results[0].status === 'fulfilled') {
        const state = results[0].value;
        note(container, `${state.total || 0} recorded · ${state.passed || 0} passed · ${state.failed || 0} failed · ${state.unconfirmed || 0} unconfirmed`);
        if (state.persistence_failed) container.append(el('p', 'Latest status could not be saved.', 'panel-error'));
        if (!state.persistence_available) note(container, 'Status persistence is unavailable.');
        if (state.dropped) note(container, `${state.dropped} older observations are outside this view.`);
        if (!state.tests?.length) note(container, 'No checks have been recorded.');
        for (const check of [...(state.tests || [])].reverse()) {
          const card = el('article', undefined, 'check-card'); card.dataset.state = check.status;
          card.append(el('strong', check.name), el('span', stateLabel(check.status), `state-badge ${check.status}`), el('p', check.file, 'muted'));
          if (check.error) disclosure(card, 'Check output', check.error);
          if (check.output_truncated) note(card, 'Output excerpt truncated.');
          container.append(card);
        }
      } else fail(container, results[0].reason);
      if (results[1].status === 'fulfilled') {
        const state = results[1].value;
        if (state.confidence_assessment_available) note(container, `Heuristic confidence: ${state.confidence_level || 'assessed'}. Confidence is not test evidence.`);
        if(state.plan_execution){
          const plan=state.plan_execution, card=el('article',undefined,'check-card');
          card.append(el('strong',`Loaded plan: ${plan.title}`),el('p',`${plan.current===false?'Previous execution':'Current execution'} · ${plan.status}`,'muted'));
          const phases=el('ol'), phaseLabels={done:'Policy accepted',running:'Working',pending:'Not started',skipped:'Skipped',failed:'Failed',deferred:'Needs review',unconfirmed:'Unconfirmed'};
          for(const phase of plan.phases||[]){const item=el('li',`${phase.title}: ${phaseLabels[phase.status]||'Unknown'}`);if(phase.note)item.append(el('p',phase.note,'muted'));phases.append(item);}
          const details=el('details');details.append(el('summary',`${(plan.phases||[]).filter(phase=>phase.status==='done').length}/${(plan.phases||[]).length} phases accepted · inspect execution`),phases);card.append(details);note(card,'Phase acceptance follows the configured completion policy; recorded tests remain separate.');container.append(card);
        }
        if (state.iterative_evaluation) {
          const loop=state.iterative_evaluation;
          note(container, `Recorded self-evaluation: ${loop.status} · iteration ${loop.iteration}/${loop.max_iterations}.`);
          if(state.active)note(container, `Current activity: ${String(state.phase).replaceAll('_',' ')}.`);
          if(loop.note)note(container,loop.note);
          note(container,'Self-evaluation acceptance reflects confidence policy; it is not a test pass.');
        } else note(container, 'No iterative evaluation has been recorded for this chat.');
      } else container.append(el('p', `Confidence status unavailable: ${results[1].reason?.message || results[1].reason}`, 'panel-error'));
    }
    async function refresh(options = {}) {
      registered = options.registered ?? registered;
      $('contract-new').disabled=!registered || !session()?.id || !contractsLoaded;
      if (!session()) return;
      if (busy) {pending = true; return;}
      busy = true;
      const id = session()?.id, generation = epoch;
      try {
        const requests = [];
        if (registered && !reviewExpanded) requests.push(loadReviews().catch(error => { if(valid(id,generation)) fail($('recorded-reviews'), error); }));
        if (!registered) renderReviews();
        if (activeTab === 'contracts') requests.push(loadContracts().catch(error => { if(valid(id,generation)) fail($('workspace-contracts'), error); }));
        if (activeTab === 'checks') requests.push(loadChecks().catch(error => { if(valid(id,generation)) fail($('workspace-checks'), error); }));
        await Promise.all(requests);
      } finally {busy = false; if (pending) {pending = false; refresh({registered});}}
    }
    function renderUsage(usage) {
      const container = $('task-usage'); container.replaceChildren(); const summary = usageSummary(usage);
      row(container, 'Tokens', summary.tokenText); row(container, 'Cost', summary.costText);
      if (usage.limits?.cost_usd) row(container, 'Cost limit', `$${usage.limits.cost_usd}`);
      if (usage.pending_executions) note(container, `${usage.pending_executions} execution(s) pending accounting.`);
      if (usage.unconfirmed_executions) container.append(el('p', `${usage.unconfirmed_executions} execution(s) have unconfirmed usage. Review is required.`, 'status-review'));
      if (usage.unknown_price) note(container, 'Pricing is unavailable or incomplete for some model calls.');
      if (usage.unknown_usage) note(container, 'Some provider usage is incomplete. Reported tokens are a lower bound.');
      if (summary.review) container.append(el('p', String(usage.reason || 'Review required').replaceAll('_', ' '), 'status-review'));
      note(container, 'Limits stop subsequent calls. Requests already running can exceed the remaining allowance.');
    }
    function renderEvents() {
      const container = $('task-events'); container.replaceChildren();
      if (!events.length) note(container, 'No events in this page.');
      for (const event of events) {
        const item = el('li', undefined, 'timeline-event'); item.append(el('strong', String(event.type).replaceAll('_', ' ')), el('small', `Revision ${event.revision} · ${new Date(event.created_at).toLocaleString()}`, 'muted'));
        if (event.payload && Object.keys(event.payload).length) disclosure(item, 'Details', event.payload);
        container.append(item);
      }
      $('task-more').hidden = !moreEvents;
    }
    function renderGraph(graph) {
      const container = $('task-dependencies'); container.replaceChildren();
      const rootNode = (graph.nodes || []).find(node => node.message?.id === graph.root_id);
      const labels = {ready:'Prerequisites satisfied',waiting:'Waiting for prerequisites',dependency_failed:'Prerequisite failed',terminal:'Request finished',active:'Request active'};
      note(container, labels[rootNode?.readiness] || 'Prerequisite state unavailable');
      for (const node of graph.nodes || []) {
        const row = el('div', undefined, 'timeline-event');
        row.append(el('strong', node.message.task_id || node.message.id), el('small', `${stateLabel(node.message.state)} · ${labels[node.readiness] || 'Unknown readiness'}`, 'muted'));
        for (const blocker of node.blockers || []) note(row, `${blocker.id}: ${String(blocker.reason || blocker.state || 'unavailable').replaceAll('_',' ')}`);
        for (const pin of node.message.required_contracts || []) note(row, `Required agreement: ${pin.contract_id} · revision ${pin.revision}`);
        for (const blocker of node.contract_blockers || []) note(row, `Agreement ${blocker.contract_id}: ${blockerText(blocker)}`);
        if (node.message.depends_on?.length) note(row, `Requires: ${node.message.depends_on.join(', ')}`);
        container.append(row);
      }
      if(graph.hidden_dependencies) note(container, 'Some prerequisites are private to other agents; their details are unavailable.');
      if(graph.truncated) note(container, 'Graph exceeds this view’s 100-request limit. Some ancestors are omitted.');
      note(container, 'Prerequisite eligibility does not reserve an agent or prove tests passed. Refresh to inspect current state.');
    }
    async function readTask(append = false) {
      const id = session()?.id, generation = epoch, taskVersion = taskGeneration, taskID = selected?.id; if (!id || !taskID) return;
      const response = await api.peerTask({sessionID: id, messageID: taskID, afterRevision: append ? revision : 0}); if (!valid(id, generation) || selected?.id !== taskID || taskGeneration !== taskVersion) return;
      selected = response.message;
      $('task-title').textContent = selected.task_id || 'Peer request'; $('task-state').textContent = stateLabel(selected.state); $('task-state').className = `state-badge ${selected.state}`;
      $('task-reference').textContent = selected.id;
      $('task-payload').textContent = typeof selected.payload?.response === 'string' ? selected.payload.response + (selected.payload.truncated ? '\n[Delivery excerpt truncated.]' : '') : readable(selected.payload);
      events = [...new Map([...(append ? events : []), ...(response.events || [])].map(item => [item.revision, item])).values()]; revision = response.next_revision; moreEvents = response.has_more; renderEvents();
    }
    async function openTask(item) {
      if(selected?.id!==item.id)usageReconciliation?.reset();
      const id = session()?.id, generation = epoch; if (!id) return;
      const taskVersion = ++taskGeneration;
      selected = item; selectedUsage=null; $('task-review').hidden=true; $('task-reconcile').hidden=true; events = []; revision = 0;
      $('task-title').textContent = item.task_id || 'Peer request'; $('task-state').textContent = 'Loading'; $('task-reference').textContent = item.id;
      $('task-dependencies').replaceChildren(); $('task-error').textContent = ''; $('task-events').replaceChildren(); $('task-usage').replaceChildren(); $('task-payload').textContent = ''; $('task-more').hidden = true;
      if (!$('task-inspector').open) $('task-inspector').showModal();
      const results = await Promise.allSettled([readTask(), api.taskUsage({sessionID: id, messageID: item.id}), Promise.resolve().then(() => api.taskGraph({sessionID: id, messageID: item.id}))]);
      if (!valid(id, generation) || selected?.id !== item.id || taskGeneration !== taskVersion) return;
      if (results[0].status === 'rejected') $('task-error').textContent = String(results[0].reason?.message || results[0].reason);
      if (results[2].status === 'fulfilled') renderGraph(results[2].value); else fail($('task-dependencies'), results[2].reason);
      if (results[1].status === 'fulfilled') {
        selectedUsage=results[1].value.usage;renderUsage(selectedUsage); $('task-reconcile').hidden=false;
        const actor=peers().find(peer=>peer.session_id===id);
        $('task-review').hidden=!(results[0].status==='fulfilled' && selected?.kind==='request' && selected.state==='blocked' && actor?.id && actor.id===selectedUsage.owner_peer_id && selectedUsage.revision>0);
      } else fail($('task-usage'), results[1].reason);
    }
    $('contract-new').addEventListener('click',()=>editContract());
    $('task-close').addEventListener('click', () => $('task-inspector').close());
    $('task-inspector').addEventListener('close', () => {selected = null; taskGeneration++;});
    $('task-more').addEventListener('click', async () => {const id=session()?.id,generation=epoch,taskVersion=taskGeneration,taskID=selected?.id;try {await readTask(true);} catch(error) {if(valid(id,generation)&&taskGeneration===taskVersion&&selected?.id===taskID) $('task-error').textContent=String(error.message || error);}});
    $('reviews-more').addEventListener('click', async () => {const id=session()?.id,generation=epoch;try {await loadReviews(true);} catch(error) {if(valid(id,generation)) fail($('recorded-reviews'),error);}});
    $('task-refresh').addEventListener('click', () => {if(selected) openTask(selected);});
    $('task-reconcile').addEventListener('click',()=>{if(!selected||!selectedUsage)return;usageReconciliation ||= (typeof module==='object'&&module.exports?require('./usage-reconciliation.js'):root.overkillUsageReconciliation).create({document:doc,api,session,onSaved:async()=>{if(selected)await openTask(selected);await refresh({registered});}});usageReconciliation.open(selected);});
    $('task-review').addEventListener('click',()=>{
      if(!selected||!selectedUsage||$('task-review').hidden)return;
      requestReview ||= (typeof module==='object'&&module.exports ? require('./request-review.js') : root.overkillRequestReview).create({document:doc,api,session,onSaved:async result=>{await openTask(result.message);await refresh({registered});}});
      requestReview.open(selected,selectedUsage);
    });
    const tabs = ['tasks', 'contracts', 'checks'];
    for (const name of tabs) {
      $(`team-tab-${name}`).addEventListener('click', () => tab(name));
      $(`team-tab-${name}`).addEventListener('keydown', event => {
        const index=tabs.indexOf(name); let next;
        if(event.key==='ArrowRight') next=tabs[(index+1)%tabs.length];
        if(event.key==='ArrowLeft') next=tabs[(index+tabs.length-1)%tabs.length];
        if(event.key==='Home') next=tabs[0]; if(event.key==='End') next=tabs.at(-1);
        if(next){event.preventDefault();tab(next);$(`team-tab-${next}`).focus();}
      });
    }
    async function refreshReviews() {const id=session()?.id,generation=epoch;reviewExpanded=false;try{await loadReviews();}catch(error){if(valid(id,generation)) fail($('recorded-reviews'),error);}}
    return {reset, refresh, openTask, refreshReviews, stateLabel};
  }
  const exported = {create, peerScopeLabel, stateLabel, presenceLabel, usageSummary, blockerText};
  if (typeof module === 'object' && module.exports) module.exports = exported; else root.overkillWorkspace = exported;
})(typeof window === 'undefined' ? globalThis : window);
