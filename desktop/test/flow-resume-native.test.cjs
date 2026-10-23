const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const {once} = require('node:events');
const {execFile} = require('node:child_process');
const {promisify} = require('node:util');
const {_electron} = require('playwright');
const exec = promisify(execFile);

test('native app automatically continues persisted work after full app restart and refreshes saved history', {
  skip: process.env.OVERKILL_REQUIRE_DESKTOP_TEST !== '1', timeout: 120000,
}, async t => {
  const directory = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'overkill-flow-resume-')));
  const workspace = path.join(directory, 'Workspace'), profile = path.join(directory, 'Profile');
  await fs.mkdir(workspace);
  let application, providerError, calls = 0;
  const toolCounts = {first: 0, second: 0};
  const fixture = http.createServer(async (request, response) => {
    try {
      let raw = ''; for await (const part of request) raw += part;
      const body = JSON.parse(raw); calls++;
      const users = body.messages.filter(message => message.role === 'user');
      assert.equal(users.length, 1, 'continuation must not duplicate the original input');
      assert.equal(users[0].content, 'Write both files and finish.');
      assert.equal(body.model, 'flow-resume-fixture');
      const has = id => body.messages.some(message => message.role === 'tool' && message.tool_call_id === id);
      let message;
      if (!has('first-file')) {
        toolCounts.first++; assert.equal(toolCounts.first, 1, 'completed first write replayed');
        message = {tool_calls: [{id: 'first-file', type: 'function', function: {name: 'fs', arguments: JSON.stringify({action: 'write', path: 'first.txt', content: 'first write exactly once\n'})}}]};
      } else if (!has('second-file')) {
        toolCounts.second++; assert.equal(toolCounts.second, 1, 'completed second write replayed');
        message = {tool_calls: [{id: 'second-file', type: 'function', function: {name: 'fs', arguments: JSON.stringify({action: 'write', path: 'second.txt', content: 'second write exactly once\n'})}}]};
      } else message = {content: 'Both files completed after app restart.'};
      const usage = {prompt_tokens: 15, completion_tokens: 5};
      if (body.stream) {
        response.writeHead(200, {'Content-Type': 'text/event-stream'});
        const delta = message.tool_calls ? {tool_calls: message.tool_calls.map((call, index) => ({index, ...call}))} : {content: message.content};
        response.end(`data: ${JSON.stringify({choices: [{index: 0, delta, finish_reason: message.tool_calls ? 'tool_calls' : 'stop'}], usage})}\n\ndata: [DONE]\n\n`);
      } else {
        response.writeHead(200, {'Content-Type': 'application/json'});
        response.end(JSON.stringify({choices: [{message: {role: 'assistant', ...message}, finish_reason: message.tool_calls ? 'tool_calls' : 'stop'}], usage}));
      }
    } catch (error) { providerError = error; response.writeHead(500); response.end('Controlled continuation failed.'); }
  });
  fixture.listen(0, '127.0.0.1'); await once(fixture, 'listening');
  t.after(async () => {
    if (application) await application.close();
    fixture.closeAllConnections(); await new Promise(resolve => fixture.close(resolve));
    await fs.rm(directory, {recursive: true, force: true});
  });
  async function launch() {
    application = await _electron.launch({args: [path.join(__dirname, 'fixtures/flow-resume.cjs'), `--overkill-profile=${profile}`], timeout: 30000});
    return await application.firstWindow();
  }
  let window = await launch();
  await window.locator('#settings[open]').waitFor();
  await window.locator('#provider').selectOption('custom'); await window.locator('#model').fill('flow-resume-fixture');
  await window.locator('#base-url').fill(`http://127.0.0.1:${fixture.address().port}/v1`);
  await window.locator('#api-key').fill('fake-flow-resume-key'); await window.locator('#settings-save').click();
  await window.locator('#settings[open]').waitFor({state: 'hidden', timeout: 45000});
  await application.evaluate(({dialog}, selected) => { dialog.showOpenDialog = async () => ({canceled: false, filePaths: [selected]}); }, workspace);
  await window.locator('#workspace').click(); await window.locator('#new-session').click();
  await window.locator('#message').fill('Write both files and finish.'); await window.locator('#send').click();
  const session = (await window.evaluate(() => window.overkill.listSessions())).sessions.find(item => item.folder === workspace);
  assert.ok(session);
  await window.waitForFunction(async id => (await window.overkill.flows(id)).flows.some(flow => flow.status === 'queued'), session.id, {timeout: 25000});
  await window.locator('#stop').waitFor({state: 'hidden'});
  assert.equal(await fs.readFile(path.join(workspace, 'first.txt'), 'utf8'), 'first write exactly once\n');
  await assert.rejects(fs.stat(path.join(workspace, 'second.txt')), {code: 'ENOENT'});
  await application.close(); application = null;
  window = await launch();
  await window.waitForFunction(() => window.overkill.state().then(state => state.ready), null, {timeout: 45000});
  await window.locator(`#sessions button[data-session-id="${session.id}"]`).click();
  await window.evaluate(() => {
    window.lastDraftInput = null;
    document.getElementById('message').addEventListener('input', event => { window.lastDraftInput = event.target.value; });
  });
  await window.locator('#message').fill('Draft kept while saved work continues.');
  // Advance only the owned fixture database's due clock. The public API has no
  // test-only resume endpoint and production still uses its normal delay.
  const advance = String.raw`import json,sys,psycopg2
state=json.load(open(sys.argv[1]))
with psycopg2.connect(host='127.0.0.1',port=state['port'],dbname='overkill',user='overkill_owner',password=state['password']) as c:
 with c.cursor() as q:q.execute("UPDATE flow_resume_jobs SET due_at=NOW() WHERE session_id=%s AND status='queued'",(sys.argv[2],))
`;
  let status;
  for (let attempts = 0; attempts < 30; attempts++) {
    if (providerError) throw providerError;
    await exec(path.resolve(__dirname, '../../.cache/audit-python/bin/python'), ['-c', advance, path.join(profile, 'backend/postgres.manager.json'), session.id]);
    const result = await window.evaluate(id => window.overkill.flows(id), session.id);
    status = result.flows[0]?.status;
    if (status === 'completed') break;
    assert.ok(!['failed', 'manual_review', 'cancelled', 'paused', 'exhausted'].includes(status), JSON.stringify(result));
    await new Promise(resolve => setTimeout(resolve, 500));
  }
  assert.equal(status, 'completed');
  await window.getByText('Both files completed after app restart.', {exact: true}).waitFor({timeout: 10000});
  await window.locator('#flow-status').filter({hasText: 'Saved work completed'}).waitFor();
  const observedDraft = await window.evaluate(() => ({current: document.getElementById('message').value, lastInput: window.lastDraftInput}));
  assert.equal(typeof observedDraft.lastInput, 'string');
  assert.ok(observedDraft.lastInput.length > 0);
  assert.equal(observedDraft.current, observedDraft.lastInput, 'history refresh must preserve the most recent input event, including any edits made while continuation runs');
  assert.equal(await fs.readFile(path.join(workspace, 'second.txt'), 'utf8'), 'second write exactly once\n');
  assert.deepEqual(toolCounts, {first: 1, second: 1}); assert.equal(calls, 3);
  const history = await window.evaluate(id => window.overkill.messages(id), session.id);
  assert.equal(history.messages.filter(item => (item.role ?? item.Role) === 'user').length, 1);
  const artifacts = path.resolve(__dirname, '../../.cache/flow-resume-native'); await fs.mkdir(artifacts, {recursive: true});
  await fs.writeFile(path.join(artifacts, 'receipt.json'), JSON.stringify({scope: 'Actual Electron and private PostgreSQL across full app restart; controlled provider, fake-only safeStorage and owned due-clock advancement.', status, calls, toolCounts, originalInputCount: 1, draftPreserved: true, history}, null, 2));
  await window.screenshot({path: path.join(artifacts, 'completed.png')});
});
