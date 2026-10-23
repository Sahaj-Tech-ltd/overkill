const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const crypto = require('node:crypto');
const {once} = require('node:events');
const {_electron} = require('playwright');

test('native think queue isolates actual file context per item and retains the complete transcript after app restart', {
  skip: process.env.OVERKILL_REQUIRE_DESKTOP_TEST !== '1', timeout: 120000,
}, async t => {
  const resources = process.env.OVERKILL_DESKTOP_RESOURCES || path.resolve(__dirname, '../../.cache/desktop-resources');
  const manifestBytes = await fs.readFile(path.join(resources, 'resource-manifest.json'));
  const manifest = JSON.parse(manifestBytes);
  const backendSHA256 = crypto.createHash('sha256').update(await fs.readFile(path.join(resources, 'bin/overkill'))).digest('hex');
  assert.equal(backendSHA256, manifest.backendSHA256, 'native test must run a manifest-bound prepared backend');
  const directory = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'overkill-sequential-context-')));
  const workspace = path.join(directory, 'Selected workspace');
  const unselected = path.join(directory, 'Unselected workspace');
  const profile = path.join(directory, 'Profile');
  await fs.mkdir(workspace); await fs.mkdir(unselected);
  const items = [
    {file: 'auth.ts', marker: 'AUTH_SELECTED_ACTUAL_BYTES_761093', foreign: 'AUTH_UNSELECTED_PRIVATE_BYTES_485912', result: 'Auth item completed'},
    {file: 'billing.ts', marker: 'BILLING_SELECTED_ACTUAL_BYTES_984251', foreign: 'BILLING_UNSELECTED_PRIVATE_BYTES_612487', result: 'Billing item completed'},
    {file: 'reports.ts', marker: 'REPORTS_SELECTED_ACTUAL_BYTES_352891', foreign: 'REPORTS_UNSELECTED_PRIVATE_BYTES_819274', result: 'Reports item completed'},
  ];
  for (const item of items) {
    item.bytes = `export const fixtureValue = '${item.marker}';\n`;
    await fs.writeFile(path.join(workspace, item.file), item.bytes);
    await fs.writeFile(path.join(unselected, item.file), `export const fixtureValue = '${item.foreign}';\n`);
  }
  const input = 'Inspect auth.ts and report its behavior\n- Inspect billing.ts and report its behavior\n- Inspect reports.ts and report its behavior';
  let application, providerError, seeded = false, providerCalls = 0;
  const observations = [];
  const fixture = http.createServer(async (request, response) => {
    try {
      let raw = ''; for await (const part of request) raw += part;
      const body = JSON.parse(raw); providerCalls++;
      assert.equal(request.headers.authorization, 'Bearer fake-sequential-context-key');
      assert.equal(body.model, 'sequential-context-fixture');
      const serialized = JSON.stringify(body);
      const seeding = !seeded;
      let content;
      if (seeding) {
        assert.ok(serialized.includes(items[0].foreign), 'seed the other session with actual different workspace bytes');
        for (const item of items) assert.ok(!serialized.includes(item.marker));
        seeded = true;
        content = 'Seed workspace inspected.';
      } else {
      const selected = items.filter(item => serialized.includes(item.marker));
      assert.equal(selected.length, 1, 'one item request must contain exactly its own actual file bytes');
      const item = selected[0], index = items.indexOf(item);
      assert.equal(index, observations.length, 'queue must process each selected file once in original order');
      assert.ok(serialized.includes(item.bytes.trim()), 'fixture must observe actual file contents, not a filename-only hint');
      for (const other of items) {
        assert.ok(!serialized.includes(other.foreign), 'unselected workspace bytes must never enter provider context');
        if (other !== item) {
          assert.ok(!serialized.includes(other.marker), 'other item file contents leaked into the active item');
          assert.ok(!serialized.includes(other.result), 'another item completion leaked into the active item');
        }
      }
      assert.ok(!serialized.includes(unselected), 'unselected workspace must not be attached to the active item');
      observations.push({item: index + 1, file: item.file, currentMarker: item.marker, otherItemMarkersAbsent: true,
        previousItemResultsAbsent: true, unselectedWorkspaceAbsent: true, stream: !!body.stream,
        requestSHA256: crypto.createHash('sha256').update(raw).digest('hex'),
        roles: body.messages.map(message => message.role)});
      content = `${item.result}: ${item.marker}`;
      }
      const usage = {prompt_tokens: 20, completion_tokens: 5};
      if (body.stream) {
        response.writeHead(200, {'Content-Type': 'text/event-stream'});
        response.end(`data: ${JSON.stringify({choices: [{index: 0, delta: {content}, finish_reason: 'stop'}], usage})}\n\ndata: [DONE]\n\n`);
      } else {
        response.writeHead(200, {'Content-Type': 'application/json'});
        response.end(JSON.stringify({choices: [{message: {role: 'assistant', content}, finish_reason: 'stop'}], usage}));
      }
    } catch (error) {
      providerError = error;
      response.writeHead(500); response.end('Controlled per-item context acceptance failed.');
    }
  });
  fixture.listen(0, '127.0.0.1'); await once(fixture, 'listening');
  t.after(async () => {
    if (application) await application.close();
    fixture.closeAllConnections(); await new Promise(resolve => fixture.close(resolve));
    await fs.rm(directory, {recursive: true, force: true});
  });
  const pageErrors = [];
  async function launch() {
    application = await _electron.launch({args: [path.join(__dirname, 'fixtures/interactive.cjs'), `--overkill-profile=${profile}`], timeout: 30000});
    const window = await application.firstWindow();
    window.on('pageerror', error => pageErrors.push(error.message));
    return window;
  }
  let window = await launch();
  await window.locator('#settings[open]').waitFor();
  await window.locator('#provider').selectOption('custom');
  await window.locator('#model').fill('sequential-context-fixture');
  await window.locator('#base-url').fill(`http://127.0.0.1:${fixture.address().port}/v1`);
  await window.locator('#api-key').fill('fake-sequential-context-key');
  await window.locator('#settings-save').click();
  await window.locator('#settings[open]').waitFor({state: 'hidden', timeout: 45000});
  await application.evaluate(({dialog}, selected) => { dialog.showOpenDialog = async () => ({canceled: false, filePaths: [selected]}); }, unselected);
  await window.locator('#workspace').click(); await window.locator('#new-session').click();
  await window.locator('#message').fill('Inspect @auth.ts from this workspace.'); await window.locator('#send').click();
  try { await window.locator('#transcript').getByText('Seed workspace inspected.', {exact: false}).last().waitFor({timeout: 20000}); }
  catch (error) { throw providerError || error; }
  await window.locator('#stop').waitFor({state: 'hidden'});
  await application.evaluate(({dialog}, selected) => { dialog.showOpenDialog = async () => ({canceled: false, filePaths: [selected]}); }, workspace);
  await window.locator('#workspace').click(); await window.locator('#new-session').click();
  await window.locator('#message').fill('/think'); await window.locator('#send').click();
  await window.locator('#transcript').getByText('Thinking on', {exact: false}).last().waitFor({timeout: 10000});
  await window.locator('#stop').waitFor({state: 'hidden'});
  await window.locator('#message').fill(input); await window.locator('#send').click();
  try {
    for (const item of items) await window.locator('#transcript').getByText(item.result, {exact: false}).last().waitFor({timeout: 20000});
    await window.locator('#stop').waitFor({state: 'hidden'});
  } catch (error) { throw providerError || error; }
  if (providerError) throw providerError;
  for (const item of items) assert.ok((await window.locator('#transcript').textContent()).includes(item.result), 'final aggregate must keep every item report visible');
  assert.equal(observations.length, 3);
  const session = (await window.evaluate(() => window.overkill.listSessions())).sessions.find(item => item.folder === workspace);
  assert.ok(session, 'selected workspace session must be persisted');
  const normalize = history => history.messages.map(message => ({role: message.role ?? message.Role, content: message.content ?? message.Content}));
  const before = normalize(await window.evaluate(id => window.overkill.messages(id), session.id));
  for (const item of items) {
    assert.ok(before.some(message => message.role === 'assistant' && message.content.includes(item.result) && message.content.includes(item.marker)), 'complete transcript must retain every item result');
    assert.ok(!JSON.stringify(before).includes(item.foreign));
  }
  const artifacts = path.resolve(__dirname, '../../.cache/sequential-context-native'); await fs.mkdir(artifacts, {recursive: true});
  await window.screenshot({path: path.join(artifacts, 'completed-before-restart.png')});
  await application.close(); application = null;
  window = await launch();
  await window.waitForFunction(() => window.overkill.state().then(state => state.ready), null, {timeout: 45000});
  await window.locator(`#sessions button[data-session-id="${session.id}"]`).click();
  for (const item of items) await window.locator('#transcript').getByText(item.result, {exact: false}).last().waitFor({timeout: 10000});
  const after = normalize(await window.evaluate(id => window.overkill.messages(id), session.id));
  assert.deepEqual(after, before, 'full app restart must retain the complete sequential transcript');
  assert.equal(observations.length, 3, 'restart must not replay completed queue items');
  assert.equal(providerCalls, 4, 'one prior workspace seed and exactly three isolated item calls');
  assert.deepEqual(pageErrors, []);
  for (const item of items) assert.equal(await fs.readFile(path.join(workspace, item.file), 'utf8'), item.bytes);
  assert.equal(crypto.createHash('sha256').update(await fs.readFile(path.join(resources, 'bin/overkill'))).digest('hex'), backendSHA256, 'prepared backend must not change during the restart acceptance');
  await fs.writeFile(path.join(artifacts, 'receipt.json'), JSON.stringify({scope: 'Actual Electron, bundled backend and private PostgreSQL; three isolated model requests and full app restart. Controlled HTTP provider, fake-only safeStorage; no live account or Keychain claim.',
    backendSHA256, resourceManifestSHA256: crypto.createHash('sha256').update(manifestBytes).digest('hex'), selectedWorkspace: workspace, unselectedWorkspace: unselected, priorWorkspaceActuallySeeded: seeded, providerCalls, isolatedItemCalls: observations.length, observations, originalQueue: input,
    files: items.map(item => ({file: item.file, bytesSHA256: crypto.createHash('sha256').update(item.bytes).digest('hex'), marker: item.marker})),
    persistedHistory: after, fullRestartHistoryEqual: true, completedItemsNotReplayed: true, pageErrors}, null, 2));
  await window.screenshot({path: path.join(artifacts, 'completed-after-restart.png')});
});
