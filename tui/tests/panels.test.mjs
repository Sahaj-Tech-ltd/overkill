import assert from 'node:assert/strict';
import test from 'node:test';
import { TodoPanelModel, SkillsPanelModel } from '../dist/components/sidebar/panel-models.js';
import React from 'react';
import { render, Box } from 'ink';
import { PassThrough, Writable } from 'node:stream';
import { TodoPanel } from '../dist/components/sidebar/todo-panel.js';
import { SkillsPanel } from '../dist/components/sidebar/skills-panel.js';

const task = (id, intent = id) => ({ id, intent, status: 'open' });
const skill = { name: 'fixture', description: 'A real fixture skill', category: '', tags: [], enabled: true };
const deferred = () => { let resolve; const promise = new Promise((done) => { resolve = done; }); return { promise, resolve }; };

test('command palette changes highlighted result counts and selects a filtered action', async () => {
  const {CommandPalette} = await import('../dist/components/dialogs/command-palette.js');
  let chosen = '';
  const commands = [
    {id:'help',title:'Keyboard Shortcuts',description:'Show all keyboard shortcuts',action:()=>{chosen='help';}},
    {id:'settings',title:'Settings',description:'Edit configuration',action:()=>{chosen='settings';}},
  ];
  const ui = mount(React.createElement(Box, {width:100,height:60},
    React.createElement(CommandPalette, {open:true,onClose:()=>{},commands})));
  try {
    await waitFor(()=>ui.output().includes('Edit configuration'));
    const before=ui.output().length;
    ui.input('s');
    await waitFor(()=>ui.output().slice(before).includes('Settings'));
    ui.input('\u007f');
    await new Promise(resolve=>setTimeout(resolve,40));
    const cleared=ui.output().length;
    ui.input('K');
    await waitFor(()=>ui.output().slice(cleared).includes('Keyboard Shortcuts'));
    ui.input('\r');
    await waitFor(()=>chosen==='help');
    assert.doesNotMatch(ui.output(), /Rendered more hooks|change in the order of Hooks/);
  } finally {ui.close();}
});

test('todo sessions discard late replies and send the active session on add/edit', async () => {
  const old = deferred();
  const calls = [];
  const backend = { async call(method, params) {
    calls.push({ method, params });
    if (method === 'todo.list') return params.session_id === 'old' ? old.promise : [task('current')];
    return task(params.id ?? 'new', params.description);
  }};
  const model = new TodoPanelModel(backend);
  const previous = model.setSession('old');
  await model.setSession('current');
  old.resolve([task('stale')]);
  await previous;
  assert.deepEqual(model.getSnapshot().todos.map((item) => item.id), ['current']);
  await model.save('A real description');
  await model.save('Edited description', 'new');
  assert.equal(model.getSnapshot().todos[1].intent, 'Edited description');
  assert.deepEqual(calls.filter((call) => call.method !== 'todo.list').map((call) => call.params.session_id), ['current', 'current']);
});

test('failed task edits and partial clear retain authoritative remaining tasks', async () => {
  const backend = { async call(method, params) {
    if (method === 'todo.list') return [task('one'), task('two'), task('three')];
    if (method === 'todo.delete' && params.id === 'one') return { status: 'deleted' };
    throw new Error('Disk unavailable');
  }};
  const model = new TodoPanelModel(backend);
  await model.setSession('active');
  assert.equal(await model.save('Unsaved edit', 'one'), false);
  assert.equal(model.getSnapshot().todos[0].intent, 'one');
  await model.toggle('one');
  assert.equal(model.getSnapshot().todos[0].status, 'open');
  await model.clear();
  assert.deepEqual(model.getSnapshot().todos.map((item) => item.id), ['two', 'three']);
  assert.match(model.getSnapshot().error, /Disk unavailable/);
});

test('failed skill toggle keeps enabled state and reports error', async () => {
  const calls = [];
  const model = new SkillsPanelModel({ async call(method, params) {
    calls.push({ method, params });
    if (method === 'skills.list') return [skill];
    throw new Error('Settings are read-only');
  }});
  await model.reload();
  await model.setEnabled('fixture', false);
  assert.equal(model.getSnapshot().skills[0].enabled, true);
  assert.match(model.getSnapshot().error, /read-only/);
  assert.deepEqual(calls[1].params, { name: 'fixture', enabled: false });
});

async function waitFor(predicate) {
  const end = Date.now() + 3000;
  while (!predicate()) {
    if (Date.now() > end) throw new Error('UI did not reach expected state');
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
}
function mount(element) {
  const stdin = new PassThrough();
  stdin.isTTY = true;
  stdin.setRawMode = () => {};
  stdin.ref = () => {}; stdin.unref = () => {};
  let output = '';
  const stdout = new Writable({ write(chunk, _encoding, done) { output += chunk.toString(); done(); } });
  stdout.columns = 100; stdout.rows = 60; stdout.isTTY = false;
  const app = render(element, { stdin, stdout, stderr: stdout, debug: true, patchConsole: false, exitOnCtrlC: false });
  return { app, input: (value) => stdin.write(value), output: () => output, close: () => { app.unmount(); stdin.end(); } };
}

test('todo keyboard collects and edits description without placeholder RPC', async () => {
  const calls = [];
  const backend = { async call(method, params) {
    calls.push({ method, params });
    if (method === 'todo.list') return [];
    return task(params.id ?? 'created', params.description);
  }};
  const ui = mount(React.createElement(TodoPanel, { active: true, backend, sessionId: 'active-session' }));
  try {
    await waitFor(() => calls.length === 1);
    await new Promise((resolve) => setTimeout(resolve, 40));
    ui.input('a');
    await waitFor(() => ui.output().includes('New task:'));
    assert.equal(calls.length, 1, 'opening editor must not create a placeholder');
    ui.input('Fix authentication');
    await waitFor(() => ui.output().includes('Fix authentication'));
    ui.input('\r');
    await waitFor(() => calls.some((call) => call.method === 'todo.add'));
    assert.equal(calls.find((call) => call.method === 'todo.add').params.description, 'Fix authentication');
    await new Promise((resolve) => setTimeout(resolve, 40));
    ui.input('j'); await new Promise((resolve) => setTimeout(resolve, 30));
    ui.input('e'); await waitFor(() => ui.output().includes('Edit task:'));
    ui.input('\u0015'); await new Promise((resolve) => setTimeout(resolve, 30));
    ui.input('Add regression tests'); await new Promise((resolve) => setTimeout(resolve, 30));
    ui.input('\r');
    await waitFor(() => calls.some((call) => call.method === 'todo.update'));
    const edit = calls.find((call) => call.method === 'todo.update');
    assert.equal(edit.params.description, 'Add regression tests');
    assert.equal(edit.params.session_id, 'active-session');
  } finally { ui.close(); }
});

test('skills panel displays failed toggle and retains enabled indicator', async () => {
  const backend = { async call(method) {
    if (method === 'skills.list') return [skill];
    throw new Error('Permission denied');
  }};
  const ui = mount(React.createElement(SkillsPanel, { active: true, backend }));
  try {
    await waitFor(() => ui.output().includes('fixture'));
    ui.input('j'); await new Promise((resolve) => setTimeout(resolve, 30));
    ui.input(' ');
    await waitFor(() => ui.output().includes('Permission denied'));
    assert.match(ui.output(), /1\/1 enabled/);
    assert.doesNotMatch(ui.output(), /0\/1 enabled/);
  } finally { ui.close(); }
});

test('compiled queue panel sends selected session and discards late previous-session replies', async () => {
  const { QueuePanel } = await import('../dist/components/sidebar/queue-panel.js');
  const old = deferred();
  const current = deferred();
  const calls = [];
  const queue = (description) => ({ active: true, total: 1, done: 0, failed: 0, items: [{index: 1, description, status: 'active', elapsed_ms: 0}] });
  const backend = { call(method, params) {
    calls.push({method, params});
    return params.session_id === 'previous' ? old.promise : current.promise;
  }};
  const ui = mount(React.createElement(QueuePanel, {backend, sessionId: 'previous'}));
  try {
    await waitFor(() => calls.length === 1);
    ui.app.rerender(React.createElement(QueuePanel, {backend, sessionId: 'selected'}));
    await waitFor(() => calls.length === 2);
    assert.deepEqual(calls, [
      {method: 'sequential.queue', params: {session_id: 'previous'}},
      {method: 'sequential.queue', params: {session_id: 'selected'}},
    ]);
    current.resolve(queue('SELECTED QUEUE'));
    await waitFor(() => ui.output().includes('SELECTED QUEUE'));
    const afterSelected = ui.output().length;
    old.resolve(queue('WRONG PREVIOUS QUEUE'));
    await new Promise((resolve) => setTimeout(resolve, 60));
    assert.doesNotMatch(ui.output().slice(afterSelected), /WRONG PREVIOUS QUEUE/);
    const beforeClear = ui.output().length;
    ui.app.rerender(React.createElement(QueuePanel, {backend, sessionId: null}));
    await waitFor(() => ui.output().slice(beforeClear).includes('No items in queue'));
    assert.doesNotMatch(ui.output().slice(beforeClear), /SELECTED QUEUE/);
    assert.equal(calls.length, 2, 'no selection must not send an unscoped poll');
  } finally { ui.close(); }
});

test('compiled status and dashboard transports carry selected session over HTTP', async () => {
  const {createServer} = await import('node:http');
  const {BackendClient} = await import('../dist/backend/client.js');
  const {SelfEvalPanel} = await import('../dist/components/sidebar/self-eval-panel.js');
  const {TestPanel} = await import('../dist/components/sidebar/test-panel.js');
  const {DashboardCard} = await import('../dist/components/dashboard/DashboardCard.js');
  const calls = [];
  const server = createServer(async (request, response) => {
    const url = new URL(request.url, 'http://fixture');
    response.setHeader('content-type', 'application/json');
    if (url.pathname === '/rpc') {
      let body = ''; for await (const chunk of request) body += chunk;
      const rpc = JSON.parse(body); calls.push({method: rpc.method, params: rpc.params});
      const verification = {total: 1, passed: 1, failed: 0, skipped: 0, canceled: 0};
      const iterative = rpc.params.session_id === 'other' ? {source: 'iterative_self_evaluation', status:'accepted',phase:'complete',iteration:2,max_iterations:6,started_at:'2026-10-22T00:00:00Z'} : null;
      const result = rpc.method === 'self.eval.status' ? {source: 'post_write_verifier', self_evaluation_available: !!iterative, iterative_evaluation:iterative, plan_execution:iterative?{title:'Prior failed plan',current:false,status:'failed',phases:[{title:'Build client',status:'done'},{title:'Check endpoint',status:'failed',note:'Endpoint check failed'},{title:'Publish result',status:'pending'}]}:null, status: iterative ? 'accepted' : 'passed', phase: iterative ? 'self_evaluation_complete' : 'passed', verification} : {tests: [{id: 'real', name: 'ACTUAL CHECK', passed: true, status: 'passed'}], ...verification, running: false};
      response.end(JSON.stringify({jsonrpc:'2.0', id:rpc.id, result}));
    } else {
      calls.push({path: url.pathname, sessionId: url.searchParams.get('session_id')});
      response.end(JSON.stringify(url.pathname === '/api/goal' ? {objective:'PERSISTED GOAL',status:'active',tokens_used:17,token_budget:100,time_used_s:3} : {title:'PERSISTED PLAN',items:[{id:'step',text:'Real step',status:'done'}]}));
    }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const backend = new BackendClient(`http://127.0.0.1:${server.address().port}`, '');
  const renderSelection = (sessionId, dashboardOpen = false) => React.createElement(Box, {flexDirection:'column',height:60},
    React.createElement(SelfEvalPanel, {backend, sessionId}),
    React.createElement(TestPanel, {backend, sessionId}),
    React.createElement(DashboardCard, {backend, sessionId, open:dashboardOpen, onClose:()=>{}}));
  const ui = mount(renderSelection('selected'));
  try {
    await waitFor(() => calls.length >= 2 && ui.output().includes('ACTUAL CHECK'));
    assert.match(ui.output(), /Iterative evaluation not recorded for this chat/);
    ui.app.rerender(renderSelection('selected', true));
    await waitFor(() => calls.length >= 4 && ui.output().includes('PERSISTED GOAL'));
    assert.deepEqual(calls.filter((c)=>c.method).map((c)=>[c.method,c.params.session_id]).sort(), [['self.eval.status','selected'],['tests.results','selected']]);
    assert.deepEqual(calls.filter((c)=>c.path).map((c)=>[c.path,c.sessionId]).sort(), [['/api/goal','selected'],['/api/plan','selected']]);
    assert.match(ui.output(), /Iterative evaluation not recorded for this chat/);
    ui.app.rerender(renderSelection('other', true));
    await waitFor(() => calls.filter((c)=>c.params?.session_id === 'other' || c.sessionId === 'other').length === 4);
    ui.app.rerender(renderSelection('other', false));
    await waitFor(() => ui.output().includes('Last loop: accepted'));
    assert.match(ui.output(), /Iteration: 2\/6/);
    assert.match(ui.output(), /Previous plan: Prior failed plan/);
    assert.match(ui.output(), /1\/3 phases accepted/);
    assert.match(ui.output(), /Check endpoint: failed/);
    assert.match(ui.output(), /Publish result: not started/);
    assert.match(ui.output(), /Confidence acceptance is not a test pass/);
    assert.doesNotMatch(ui.output(), /Full self-evaluation loop status unavailable/);
  } finally {ui.close();server.closeAllConnections();await new Promise((resolve)=>server.close(resolve));}
});

test('compiled status/dashboard selection ignores late results and unmount cancels polling', async () => {
  const {SelfEvalPanel} = await import('../dist/components/sidebar/self-eval-panel.js');
  const {TestPanel} = await import('../dist/components/sidebar/test-panel.js');
  const {DashboardCard} = await import('../dist/components/dashboard/DashboardCard.js');
  const pending = [];
  const backend = {
    call(method, params, options) {const d=deferred();pending.push({...d,method,params,signal:options.signal});return d.promise;},
    get(path, params, options) {const d=deferred();pending.push({...d,path,params,signal:options.signal});return d.promise;},
  };
  const element=(sessionId)=>React.createElement(React.Fragment,null,
    React.createElement(SelfEvalPanel,{backend,sessionId}),React.createElement(TestPanel,{backend,sessionId}),
    React.createElement(DashboardCard,{backend,sessionId,open:true,onClose:()=>{}}));
  const ui=mount(element('old'));
  try {
    await waitFor(()=>pending.length===4);
    ui.app.rerender(element('new'));
    await waitFor(()=>pending.length===8);
    assert.ok(pending.slice(0,4).every((p)=>p.signal.aborted));
    for(const p of pending.slice(0,4)) p.resolve(p.path==='/api/goal'?{objective:'STALE GOAL',status:'active',tokens_used:0,token_budget:1,time_used_s:0}:p.path?{title:'STALE PLAN',items:[]}:p.method==='tests.results'?{tests:[{id:'stale',name:'STALE CHECK'}],total:1,passed:0,failed:1,running:false}:{status:'STALE STATE',verification:{passed:0,failed:1,skipped:0,canceled:0}});
    await new Promise((resolve)=>setTimeout(resolve,40));
    assert.doesNotMatch(ui.output(), /STALE (GOAL|PLAN|CHECK|STATE)/);
  } finally {ui.close();}
  assert.ok(pending.every((p)=>p.signal.aborted),'all outstanding transports aborted on cleanup');
});

test('health monitoring detects disconnect, reconnects after restart, and aborts on cleanup', async () => {
  const {createServer} = await import('node:http');
  const {Text} = await import('ink');
  const {BackendClient} = await import('../dist/backend/client.js');
  const {useBackend} = await import('../dist/hooks/use-backend.js');
  let requests=0;
  let server=createServer((_request,response)=>{requests++;response.end('ok');});
  await new Promise((resolve)=>server.listen(0,'127.0.0.1',resolve));
  const port=server.address().port;
  const backend=new BackendClient(`http://127.0.0.1:${port}`, '');
  function Probe(){const {connected}=useBackend({client:backend,healthyIntervalMs:20,retryInitialMs:10,maxRetryMs:30});return React.createElement(Text,null,`connection:${connected}`);}
  const ui=mount(React.createElement(Probe));
  try {
    await waitFor(()=>ui.output().includes('connection:connected'));
    await waitFor(()=>requests>=2); // monitoring must continue after first success
    server.closeAllConnections();await new Promise((resolve)=>server.close(resolve));
    const before=ui.output().length;
    await waitFor(()=>ui.output().slice(before).includes('connection:disconnected'));
    server=createServer((_request,response)=>{requests++;response.end('ok');});
    await new Promise((resolve)=>server.listen(port,'127.0.0.1',resolve));
    const restart=ui.output().length;
    await waitFor(()=>ui.output().slice(restart).includes('connection:connected'));
  } finally {ui.close();server.closeAllConnections();await new Promise((resolve)=>server.close(resolve));}
  let entered=0,closed=0;
  const held=createServer((_request,response)=>{entered++;response.on('close',()=>closed++);});
  await new Promise((resolve)=>held.listen(0,'127.0.0.1',resolve));
  const heldClient=new BackendClient(`http://127.0.0.1:${held.address().port}`, '');
  function HeldProbe(){useBackend({client:heldClient,healthyIntervalMs:10,retryInitialMs:10,maxRetryMs:20});return React.createElement(Text,null,'waiting');}
  const heldUI=mount(React.createElement(HeldProbe));
  try {await waitFor(()=>entered===1);heldUI.close();await waitFor(()=>closed===1);await new Promise((resolve)=>setTimeout(resolve,60));assert.equal(entered,1,'cleanup must prevent rescheduling');
    const start=Date.now();assert.equal(await heldClient.health(undefined,30),false);assert.ok(Date.now()-start<500,'held health request must be bounded');
  } finally {heldUI.close();held.closeAllConnections();await new Promise((resolve)=>held.close(resolve));}
});
