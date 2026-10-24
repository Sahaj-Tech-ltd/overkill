'use strict';
// The sandboxed emulator can report keys and geometry, never an RPC method,
// owner or chat. This controller binds every request to the selected chat.
window.overkillTerminals = (() => {
  const $ = id => document.getElementById(id), frame = $('live-terminal-frame');
  let sessionID = null, connected = false, shown = false, liveView = true;
  let generation = 0, token = '', selected = null, inventory = [], cursor = 0;
  let frameLoaded = false, frameReady = false, polling = false, actionBusy = false;
  let nextList = 0, inputEpoch = 0, queuedBytes = 0, inputQueue = Promise.resolve();
  let outputAck = null, serial = 0, inputBlocked = false;
  const positions=new Map();
  const ownerLabel = worker => window.overkillWorkers?.label(worker) || (worker==='main'?'Main chat':worker);
  const errorText = value => String(value?.message || value).replace(/^Error invoking remote method '[^']+': (?:Error: )?/, '');
  function send(type, data = {}) { if(type==='control')data={...data,controlRevision:selected?.control_revision||null,inputEpoch};if (frameLoaded) frame.contentWindow.postMessage({type, token, ...data}, '*'); }
  function current(epoch, id = selected?.id) { return epoch === generation && connected && !!sessionID && id === selected?.id; }
  function request(operation, extra = {}, sid = sessionID) { return window.overkill.terminal({operation, sessionID: sid, ...extra}); }
  function status(text) { $('live-terminal-status').textContent = text; }
  function adopt(info){
    if(!info)return;
    if(selected?.id===info.id){
      if(info.control_revision<selected.control_revision)return;
      if(info.control_revision===selected.control_revision){
        info={...info,input_sequence:Math.max(info.input_sequence||0,selected.input_sequence||0)};
        if(['closing','closed','exited'].includes(selected.state)&&info.state==='running')info.state=selected.state;
      }
    }
    selected=info;
  }
  function position(info){
    let value=positions.get(info.id);
    if(!value||value.revision!==info.control_revision){value={revision:info.control_revision,sequence:info.input_sequence||0};positions.set(info.id,value);}
    else value.sequence=Math.max(value.sequence,info.input_sequence||0);
    return value;
  }
  function pauseInput(error){inputBlocked=true;inputEpoch++;$('live-terminal-note').textContent='Input paused: '+errorText(error)+' Inspect output, then check input state. Queued keys have been discarded and will not be resent.';controls();}
  function controls() {
    const running = connected && selected?.state === 'running', human = selected?.controller === 'human';
    $('terminal-keep').disabled=!running||!selected?.can_handoff||actionBusy;
    $('terminal-reconnect-input').disabled=!running||!human||!inputBlocked||actionBusy;
    $('terminal-take-control').disabled = !running || human || actionBusy;
    $('terminal-return-control').disabled = !running || !human || actionBusy;
    $('terminal-interrupt').disabled = !running || !human || actionBusy;
    $('terminal-end').disabled = !running || actionBusy;
    $('terminal-forget').disabled = !selected || running || selected?.state === 'closing' || actionBusy;
    $('live-terminal-select').disabled = !inventory.length || actionBusy;
    send('control', {enabled: !!(running && human && shown && liveView && !actionBusy && !inputBlocked), focus: false});
  }
  function describe() {
    if (!selected) return;
    const who = selected.controller === 'human' ? 'You have control' : 'Agent has control';
    status(`${ownerLabel(selected.owner.worker)} · ${selected.state}${selected.exit_code !== undefined ? ` · exit ${selected.exit_code}` : ''} · ${who}`);
    $('live-terminal-command').textContent = selected.command;
    if (selected.cleanup_error) $('live-terminal-note').textContent = selected.cleanup_error;
    controls();
  }
  function reset() {
    generation++; inputEpoch++; queuedBytes = 0; inputQueue = Promise.resolve();
    cursor = 0; frameReady = false; inputBlocked=false;positions.clear();token = crypto.randomUUID();
    if (outputAck) { outputAck.finish(false); outputAck = null; }
    send('init'); controls();
  }
  function choose(id) {
    selected = inventory.find(item => item.id === id) || null;
    reset(); $('live-terminal-command').textContent = selected?.command || '';
    if (selected) describe();
    else status(connected ? 'No live terminals. Ask this chat’s agent to start one.' : 'Reconnect to view terminals. Old processes cannot be restored after a backend restart.');
  }
  function renderInventory(items) {
    inventory = items;
    const picker = $('live-terminal-select'); picker.replaceChildren();
    for (const item of items) {
      const option = document.createElement('option'); option.value = item.id;
      option.textContent = `${ownerLabel(item.owner.worker)} · ${item.command.slice(0, 64)} · ${item.state}`;
      picker.append(option);
    }
    const same = items.find(item => item.id === selected?.id);
    if (same) { adopt(same); picker.value = same.id; describe(); }
    else { choose(items[0]?.id); picker.value = selected?.id || ''; }
    controls();
  }
  function writeOutput(bytes, epoch) {
    return new Promise(resolve => {
      const number = ++serial;
      const timer = setTimeout(() => { if (outputAck?.serial === number) outputAck = null; resolve(false); }, 5000);
      outputAck = {serial: number, finish(ok) { clearTimeout(timer); resolve(ok && epoch === generation); }};
      send('output', {bytes, serial: number});
    });
  }
  async function poll() {
    if (polling || !sessionID || !connected || document.hidden) return;
    polling = true;
    let epoch = generation;
    try {
      if (Date.now() >= nextList) {
        const list = await request('list');
        if (epoch !== generation) return;
        nextList = Date.now() + 1500;
        renderInventory(list.terminals || []); epoch = generation;
        if (!inventory.length) status(list.reason || (list.available ? 'No live terminals. Ask this chat’s agent to start one.' : 'Terminals are unavailable for this chat.'));
      }
      if (!shown || !liveView || !selected || !frameReady) return;
      const id = selected.id;
      const chunk = await request('read', {id, cursor, limit: 32768, waitMS: 200});
      if (!current(epoch, id)) return;
      adopt(chunk.terminal);
      if (chunk.truncated) { send('reset'); $('live-terminal-note').textContent = 'Earlier output expired from the bounded buffer.'; }
      if (chunk.data && !await writeOutput(chunk.data, epoch)) throw new Error('Terminal display did not acknowledge output. Reopen the terminal to reconnect.');
      if (!current(epoch, id)) return;
      cursor = chunk.cursor; describe();
    } catch (error) { if (epoch === generation) status(errorText(error)); }
    finally { polling = false; }
  }
  async function action(operation, extra = {}) {
    if (!selected || !connected || actionBusy) return;
    const epoch = generation, id = selected.id, sid = sessionID;
    actionBusy = true; inputEpoch++; controls();
    try {
      const revision=selected.control_revision;
      let result;
      if(operation==='interrupt'){
        await inputQueue;
        if(!current(epoch,id)||selected.control_revision!==revision)return;
        const next=position(selected);result=await request(operation,{id,controlRevision:revision,sequence:++next.sequence},sid);
        if(!result.success){pauseInput(new Error(result.error||'Input outcome is uncertain.'));return;}
        if(current(epoch,id))selected.input_sequence=Math.max(selected.input_sequence||0,result.sequence);
      }else result=await request(operation,{id,controlRevision:revision,...extra},sid);
      if (current(epoch, id)) {
        nextList = 0;
        if(operation==='handoff'){adopt(result.terminal);if(!result.success)pauseInput(new Error(result.error));else $('live-terminal-note').textContent='Ownership transferred to Main chat. Original lifetime and task limits remain in force.';}
        if (operation === 'control') {adopt(result);position(selected);inputBlocked=false;}
        if (operation === 'close' && selected.state==='running') selected.state = 'closing';
        describe();
        if (operation === 'control' && !extra.release) send('control', {enabled: shown && liveView, focus: true});
      }
    } catch (error) { if (current(epoch, id)) { if(operation==='interrupt')pauseInput(error);else status(errorText(error)); } }
    finally { actionBusy = false; controls(); void poll(); }
  }
  function queueInput(data) {
    if (!selected || selected.controller !== 'human' || selected.state !== 'running' || !connected || !shown || !liveView || actionBusy || inputBlocked) return;
    const size = data.bytes === undefined ? new TextEncoder().encode(data.data).length : atob(data.bytes).length;
    if (size > 16384 || queuedBytes + size > 32768) { status('Input is too large. Paste at most 16 KiB at a time.'); return; }
    const epoch = generation, controlEpoch = inputEpoch, sid = sessionID, id = selected.id, revision=selected.control_revision;
    queuedBytes += size;
    inputQueue = inputQueue.then(async () => {
      if (!current(epoch, id) || controlEpoch !== inputEpoch || selected.controller !== 'human' || selected.control_revision!==revision || inputBlocked) return;
      const next=position(selected);
      const receipt=await request('input',{id,...data,controlRevision:revision,sequence:++next.sequence},sid);
      if(!current(epoch,id))return;
      if(!receipt.success){pauseInput(new Error(receipt.error||'Input outcome is uncertain.'));return;}
      if(selected.control_revision===revision)selected.input_sequence=Math.max(selected.input_sequence||0,receipt.sequence);
    }).catch(error => { if (current(epoch, id)) pauseInput(error); }).finally(() => { if (epoch === generation) queuedBytes -= size; });
  }
  window.addEventListener('message', event => {
    if (event.source !== frame.contentWindow || !event.data || event.data.token !== token) return;
    const value = event.data;
    if (value.type === 'ready') { frameReady = true; controls(); void poll(); }
    if (value.type === 'written' && outputAck?.serial === value.serial) { outputAck.finish(true); outputAck = null; }
    if (value.type === 'input' && typeof value.data === 'string' && value.controlRevision===selected?.control_revision && value.inputEpoch===inputEpoch) queueInput({data: value.data});
    if (value.type === 'binary' && typeof value.bytes === 'string' && value.controlRevision===selected?.control_revision && value.inputEpoch===inputEpoch) { try { queueInput({bytes: value.bytes}); } catch {} }
    if (value.type === 'resize' && value.controlRevision===selected?.control_revision && value.inputEpoch===inputEpoch && selected?.controller === 'human' && !actionBusy && !inputBlocked && connected && Number.isInteger(value.rows) && Number.isInteger(value.cols) && value.rows > 0 && value.cols > 0 && value.rows <= 1000 && value.cols <= 1000) {
      const epoch = generation, id = selected.id;
      request('resize', {id, controlRevision:selected.control_revision, rows: value.rows, cols: value.cols}).catch(error => { if (current(epoch, id)) status(errorText(error)); });
    }
  });
  frame.addEventListener('load', () => { frameLoaded = true; reset(); });
  $('live-terminal-select').addEventListener('change', event => { choose(event.target.value); void poll(); });
  $('terminal-reconnect-input').addEventListener('click',async()=>{
    if(!selected||!connected||actionBusy)return;const epoch=generation,id=selected.id;actionBusy=true;inputEpoch++;controls();
    try{const chunk=await request('read',{id,cursor,limit:1});if(!current(epoch,id))return;adopt(chunk.terminal);positions.delete(id);position(selected);inputBlocked=false;$('live-terminal-note').textContent='Fresh input state loaded. Previous queued keys were not resent.';describe();}
    catch(error){if(current(epoch,id))pauseInput(error);}
    finally{actionBusy=false;controls();}
  });
  $('terminal-take-control').addEventListener('click', () => action('control'));
  $('terminal-return-control').addEventListener('click', () => action('control', {release: true}));
  $('terminal-interrupt').addEventListener('click', () => action('interrupt'));
  $('terminal-end').addEventListener('click', () => action('close'));
  $('terminal-keep').addEventListener('click',()=>action('handoff'));
  $('terminal-forget').addEventListener('click', () => action('forget'));
  for (const [id, live] of [['terminal-live-tab', true], ['terminal-log-tab', false]]) $(id).addEventListener('click', () => {
    liveView = live; $('live-terminal').hidden = !live; $('terminal-output').hidden = live;
    $('terminal-live-tab').setAttribute('aria-selected', String(live)); $('terminal-log-tab').setAttribute('aria-selected', String(!live)); controls(); void poll();
  });
  setInterval(() => void poll(), 300);
  controls();
  return {
    select(session) { sessionID = session?.id || null; inventory = []; selected = null; nextList = 0; reset(); renderInventory([]); $('live-terminal-note').textContent = ''; void poll(); },
    connection(value) { if (connected === value) return; connected = value; nextList = 0; reset(); if (!value) { inventory = []; selected = null; renderInventory([]); } void poll(); },
    visible(value) { shown = value; inputEpoch++; controls(); if (value) { nextList = 0; void poll(); } }
  };
})();
