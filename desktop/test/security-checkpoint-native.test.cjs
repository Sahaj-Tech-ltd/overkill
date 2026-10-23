const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const {once} = require('node:events');
const {_electron} = require('playwright');

test('native app enforces planning and shell admission, captures and restores an actual edit', {
  skip: process.env.OVERKILL_REQUIRE_DESKTOP_TEST !== '1', timeout: 90000,
}, async t => {
  const directory = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'overkill-security-checkpoint-')));
  const workspace = path.join(directory, 'Workspace');
  await fs.mkdir(path.join(workspace, 'protected'), {recursive: true});
  await fs.writeFile(path.join(workspace, 'protected/sentinel.txt'), 'must remain');
  await fs.writeFile(path.join(workspace, 'source.txt'), 'original content\n', {mode: 0o755});
  let application, checkpointID, providerError;
  const observations = [];
  const toolResult = (body, id) => body.messages.findLast(message => message.role === 'tool' && message.tool_call_id === id)?.content;
  const fixture = http.createServer(async (request, response) => {
    try {
      let raw = ''; for await (const part of request) raw += part;
      const body = JSON.parse(raw);
      assert.equal(request.headers.authorization, 'Bearer fake-security-checkpoint-key');
      const prompt = body.messages.findLast(message => message.role === 'user')?.content || '';
      const send = message => {
        const usage = {prompt_tokens: 15, completion_tokens: 5};
        if (body.stream) {
          response.writeHead(200, {'Content-Type': 'text/event-stream'});
          const delta = message.tool_calls ? {tool_calls: message.tool_calls.map((call, index) => ({index, ...call}))} : {content: message.content};
          response.end(`data: ${JSON.stringify({choices: [{index: 0, delta, finish_reason: message.tool_calls ? 'tool_calls' : 'stop'}], usage})}\n\ndata: [DONE]\n\n`);
        } else {
          response.writeHead(200, {'Content-Type': 'application/json'});
          response.end(JSON.stringify({choices: [{message: {role: 'assistant', ...message}, finish_reason: message.tool_calls ? 'tool_calls' : 'stop'}], usage}));
        }
      };
      const tool = (id, name, input) => send({tool_calls: [{id, type: 'function', function: {name, arguments: JSON.stringify(input)}}]});
      if (prompt === 'Try planning mutation.') {
        const result = toolResult(body, 'planning-write');
        if (!result) return tool('planning-write', 'fs', {action: 'write', path: 'source.txt', content: 'forbidden'});
        assert.match(result, /write denied|read-only mode/);
        observations.push({case: 'plan_write_denied', result});
        return send({content: 'Planning mutation blocked.'});
      }
      if (prompt === 'Try a write without an approval client.') {
        const result = toolResult(body, 'safe-write');
        if (!result) return tool('safe-write', 'fs', {action: 'write', path: 'source.txt', content: 'unapproved'});
        assert.match(result, /denied by user/);
        observations.push({case: 'missing_approval_denied', result});
        return send({content: 'Unapproved mutation blocked.'});
      }
      if (prompt === 'Exercise shell admission.') {
        const result = toolResult(body, 'shell-hazard');
        // The target is an owned disposable sentinel, never a host/system path.
        if (!result) return tool('shell-hazard', 'shell', {command: 'rm -f -r ./protected', working_dir: workspace});
        assert.match(result, /blocked.*scanner|recursive force/);
        observations.push({case: 'shell_hazard_denied', result});
        return send({content: 'Shell hazard rejected.'});
      }
      if (prompt === 'Apply checkpointed edit.') {
        if (!toolResult(body, 'captured-edit')) return tool('captured-edit', 'fs', {action: 'write', path: 'source.txt', content: 'changed content\n'});
        const listed = toolResult(body, 'list-captures');
        if (!listed) return tool('list-captures', 'checkpoint_list', {});
        const {checkpoints} = JSON.parse(listed);
        const manifest = checkpoints.find(item => item.reason === 'pre-fs auto-snapshot' && item.entries.some(entry => entry.path === path.join(workspace, 'source.txt') && entry.existed));
        assert.ok(manifest, 'real runtime must publish an automatic pre-write capture');
        checkpointID = manifest.id;
        observations.push({case: 'automatic_capture', manifest});
        return send({content: 'Checkpointed edit saved.'});
      }
      if (prompt === 'Restore the captured source.') {
        const result = toolResult(body, 'restore-source');
        if (!result) return tool('restore-source', 'checkpoint_restore', {id: checkpointID});
        assert.equal(JSON.parse(result).restored, checkpointID);
        observations.push({case: 'actual_restore', result: JSON.parse(result)});
        return send({content: 'Original file restored.'});
      }
      throw new Error(`Unexpected provider request: ${prompt.slice(0, 150)}`);
    } catch (error) { providerError = error; response.writeHead(500); response.end('Controlled acceptance failed.'); }
  });
  fixture.listen(0, '127.0.0.1'); await once(fixture, 'listening');
  t.after(async () => {
    if (application) await application.close();
    fixture.closeAllConnections(); await new Promise(resolve => fixture.close(resolve));
    await fs.rm(directory, {recursive: true, force: true});
  });
  application = await _electron.launch({args: [path.join(__dirname, 'fixtures/interactive.cjs'), `--overkill-profile=${path.join(directory, 'Profile')}`], timeout: 30000});
  const window = await application.firstWindow();
  const pageErrors = []; window.on('pageerror', error => pageErrors.push(error.message));
  await window.locator('#settings[open]').waitFor();
  await window.locator('#provider').selectOption('custom');
  await window.locator('#model').fill('security-checkpoint-fixture');
  await window.locator('#base-url').fill(`http://127.0.0.1:${fixture.address().port}/v1`);
  await window.locator('#api-key').fill('fake-security-checkpoint-key');
  await window.locator('#settings-save').click();
  await window.locator('#settings[open]').waitFor({state: 'hidden', timeout: 45000});
  await application.evaluate(({dialog}, selected) => { dialog.showOpenDialog = async () => ({canceled: false, filePaths: [selected]}); }, workspace);
  await window.locator('#workspace').click(); await window.locator('#new-session').click();
  async function submit(message, expected) {
    await window.locator('#message').fill(message); await window.locator('#send').click();
    await window.locator('#transcript').getByText(expected, {exact: false}).last().waitFor({timeout: 20000});
    await window.locator('#stop').waitFor({state: 'hidden'});
    if (providerError) throw providerError;
  }
  await submit('Exercise shell admission.', 'Shell hazard rejected.');
  assert.equal(await fs.readFile(path.join(workspace, 'protected/sentinel.txt'), 'utf8'), 'must remain');
  await submit('/plan', 'read-only tools only');
  await submit('Try planning mutation.', 'Planning mutation blocked.');
  assert.equal(await fs.readFile(path.join(workspace, 'source.txt'), 'utf8'), 'original content\n');
  await submit('/safe', 'Safe mode');
  await submit('Try a write without an approval client.', 'Unapproved mutation blocked.');
  assert.equal(await fs.readFile(path.join(workspace, 'source.txt'), 'utf8'), 'original content\n');
  await submit('/yolo', 'YOLO mode');
  await submit('Apply checkpointed edit.', 'Checkpointed edit saved.');
  assert.equal(await fs.readFile(path.join(workspace, 'source.txt'), 'utf8'), 'changed content\n');
  await submit('Restore the captured source.', 'Original file restored.');
  assert.equal(await fs.readFile(path.join(workspace, 'source.txt'), 'utf8'), 'original content\n');
  assert.equal((await fs.stat(path.join(workspace, 'source.txt'))).mode & 0o777, 0o755);
  assert.deepEqual(pageErrors, []);
  assert.deepEqual(observations.map(row => row.case), ['shell_hazard_denied', 'plan_write_denied', 'missing_approval_denied', 'automatic_capture', 'actual_restore']);
  const artifacts = path.resolve(__dirname, '../../.cache/security-checkpoint-native'); await fs.mkdir(artifacts, {recursive: true});
  await fs.writeFile(path.join(artifacts, 'receipt.json'), JSON.stringify({scope: 'Actual Electron, bundled backend and private PostgreSQL; controlled provider and fake-only safeStorage. No installed Keychain or paid-provider acceptance.', observations, originalModeRestored: true, pageErrors}, null, 2));
  await window.screenshot({path: path.join(artifacts, 'workspace.png')});
});
