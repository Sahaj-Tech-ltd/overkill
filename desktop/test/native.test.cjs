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

const required = process.env.OVERKILL_REQUIRE_DESKTOP_TEST === '1';
const mockStorageSmoke = process.env.OVERKILL_INSTALLED_MOCK_STORAGE_SMOKE === '1';
if (mockStorageSmoke && (!required || !path.isAbsolute(process.env.OVERKILL_INSTALLED_DESKTOP_EXECUTABLE || ''))) {
  throw new Error('Mock storage smoke requires an explicit installed executable and mandatory desktop test mode.');
}

test('native window uses bundled backend/database, writes a file, and restores chat after relaunch', {skip: !required, timeout: 120000}, async t => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'overkill-native-'));
  const home = path.join(directory, 'App Data With Spaces'); const workspace = path.join(directory, 'Workspace With Spaces');
  await fs.mkdir(home); await fs.mkdir(workspace);
  let calls = 0, hanging = false, cancelled = false, coordinationCalls = 0, frontendID, releaseCoordination;
  const fixture = http.createServer(async (request, response) => {
    const chunks = []; for await (const chunk of request) chunks.push(chunk);
    const body = JSON.parse(Buffer.concat(chunks).toString());
    assert.equal(request.headers.authorization, 'Bearer desktop-acceptance-key');
    assert.ok(!JSON.stringify(body.messages).includes('desktop-acceptance-key'));
    calls++;
    const latestUser = body.messages.filter(message => message.role === 'user').at(-1);
    if (latestUser?.content === 'Wait until I stop this response.') {
      hanging = true;
      response.writeHead(200, {'Content-Type': 'text/event-stream'}); response.flushHeaders();
      response.on('close', () => { cancelled = true; });
      return;
    }
    if (typeof latestUser?.content === 'string' && latestUser.content.startsWith('A bound peer sent this coordination message.')) {
      const envelope = JSON.parse(latestUser.content.slice(latestUser.content.indexOf('\n') + 1));
      const content = envelope.kind === 'request' ? 'Backend update: the local endpoint is ready.' : 'Frontend received the backend update.';
      if (envelope.kind === 'request') {
        coordinationCalls++;
        await new Promise(resolve => { releaseCoordination = resolve; });
      }
      response.writeHead(200, {'Content-Type': 'application/json'});
      response.end(JSON.stringify({choices: [{message: {role: 'assistant', content}, finish_reason: 'stop'}], usage: {prompt_tokens: 30, completion_tokens: 12}}));
      return;
    }
    const hasToolResult = body.messages.some(message => message.role === 'tool');
    const delta = hasToolResult ? {content: 'The workspace file is saved.'} : {tool_calls: [{index: 0, id: 'desktop-write', type: 'function', function: {name: 'fs', arguments: JSON.stringify({action: 'write', path: 'desktop-acceptance.txt', content: 'native desktop verified\n'})}}]};
    response.writeHead(200, {'Content-Type': 'text/event-stream'});
    response.write(`data: ${JSON.stringify({id: 'fixture', choices: [{index: 0, delta, finish_reason: null}]})}\n\n`);
    response.write(`data: ${JSON.stringify({id: 'fixture', choices: [{index: 0, delta: {}, finish_reason: hasToolResult ? 'stop' : 'tool_calls'}], usage: {prompt_tokens: 30, completion_tokens: 12}})}\n\n`);
    response.end('data: [DONE]\n\n');
  });
  fixture.listen(0, '127.0.0.1'); await once(fixture, 'listening');
  let application;
  async function closeApplication() {
    if (!application) return;
    const owned = application; let timer;
    try {
      await Promise.race([owned.close(), new Promise((_, reject) => { timer = setTimeout(() => reject(new Error('Owned desktop did not shut down within 20 seconds.')), 20000); })]);
    } catch (error) {
      // Only terminate the exact process created by this disposable test.
      try { owned.process().kill('SIGKILL'); } catch {}
      throw error;
    } finally { clearTimeout(timer); application = null; }
  }
  t.after(async () => {
    let closeError;
    if (application) {
      const active = application.windows()[0];
      if (active) {
        t.diagnostic(JSON.stringify(await active.evaluate(async () => ({ready: (await window.overkill.state()).ready, alert: document.getElementById('error').textContent})).catch(() => ({}))));
        await active.screenshot({path: path.resolve(__dirname, '../../.cache/desktop-native-failure.png')}).catch(() => {});
      }
      try { await closeApplication(); } catch (error) { closeError = error; }
    }
    releaseCoordination?.();
    fixture.close();
    const postgresBin = path.resolve(__dirname, '../../.cache/desktop-resources/postgres/bin');
    await promisify(execFile)(path.join(postgresBin, 'pg_ctl'), ['-D', path.join(home, 'backend/postgres'), '-m', 'immediate', '-w', 'stop']).catch(() => {});
    await fs.rm(directory, {recursive: true, force: true});
    if (closeError) throw closeError;
  });
  const installedExecutable = process.env.OVERKILL_INSTALLED_DESKTOP_EXECUTABLE;
  const launch = async () => {
    // Mock storage is restricted to this disposable fixture profile. Hide its
    // fake credential during startup because app readiness can precede the
    // Playwright evaluate call. Restore it only after installing the fake-only
    // methods; relaunch below explicitly reconnects through the settings UI.
    const saved = path.join(home, 'settings.json');
    const held = path.join(home, 'settings.mock-smoke-held.json');
    let heldSettings = false;
    if (mockStorageSmoke) {
      try { await fs.rename(saved, held); heldSettings = true; }
      catch (error) { if (error.code !== 'ENOENT') throw error; }
    }
    let launched;
    try {
      launched = await _electron.launch({
        ...(installedExecutable ? {executablePath: installedExecutable} : {}),
        args: [...(installedExecutable ? [] : [path.resolve(__dirname, '..')]), `--overkill-profile=${home}`],
        env: {...process.env, OVERKILL_DESKTOP_DATA_DIR: path.join(directory, 'Ignored Development Override'), OVERKILL_DESKTOP_RESOURCES: installedExecutable ? path.join(directory, 'Missing Resources') : process.env.OVERKILL_DESKTOP_RESOURCES},
        timeout: 30000,
      });
      if (mockStorageSmoke) {
        const installed = await launched.evaluate(({app, safeStorage}) => {
          if (!app.isPackaged) throw new Error('Mock storage smoke requires a packaged application.');
          const fakeKey = 'desktop-acceptance-key';
          const fixture = Buffer.from('MOCK-STORAGE-SMOKE-ONLY:' + [...fakeKey].reverse().join(''));
          const calls = globalThis.installedMockStorageSmoke = {encrypt: 0, decrypt: 0};
          safeStorage.isAsyncEncryptionAvailable = async () => true;
          safeStorage.encryptStringAsync = async value => {
            if (value !== fakeKey) throw new Error('Smoke storage refuses non-fixture credentials.');
            calls.encrypt++;
            return Buffer.from(fixture);
          };
          safeStorage.decryptStringAsync = async value => {
            if (!Buffer.isBuffer(value) || !value.equals(fixture)) throw new Error('Smoke storage refuses non-fixture data.');
            calls.decrypt++;
            return {result: fakeKey, shouldReEncrypt: false};
          };
          return {packaged: app.isPackaged, mode: 'fake-only mock storage; no encryption or Keychain acceptance'};
        });
        assert.equal(installed.packaged, true);
        t.diagnostic(installed.mode);
      }
      if (heldSettings) {
        const startup = await launched.firstWindow();
        await startup.waitForFunction(async () => !(await window.overkill.state()).starting);
      }
      return launched;
    } catch (error) {
      if (launched) await launched.close().catch(() => {});
      throw error;
    } finally {
      if (heldSettings) await fs.rename(held, saved);
    }
  };
  application = await launch();
  assert.deepEqual(await application.evaluate(({app}) => ({packaged: app.isPackaged, profile: app.getPath('userData')})), {packaged: !!installedExecutable, profile: home});
  let window = await application.firstWindow();
  await window.locator('dialog[open]').waitFor();
  assert.equal(await window.evaluate(() => typeof require), 'undefined');
  assert.equal(await window.evaluate(() => typeof process), 'undefined');
  assert.equal(await window.evaluate(() => 'call' in window.overkill), false);
  await window.locator('#model').fill('desktop-fixture-model');
  await window.locator('#base-url').fill(`http://127.0.0.1:${fixture.address().port}/v1`);
  await window.evaluate(() => {
    document.getElementById('settings-form').addEventListener('submit', () => {
      (globalThis.fixtureSubmitKeyLengths ??= []).push(document.getElementById('api-key').value.length);
    }, {capture: true});
  });
  await window.locator('#api-key').fill('desktop-acceptance-key');
  await window.locator('#settings-save').click();
  await window.waitForFunction(() => !document.getElementById('settings').open || (!document.getElementById('settings-save').disabled && document.getElementById('settings-error').textContent), null, {timeout:45000});
  assert.equal(await window.locator('#settings').evaluate(node => node.open), false,
    JSON.stringify(await window.evaluate(() => ({error:document.getElementById('settings-error').textContent,submitKeyLengths:globalThis.fixtureSubmitKeyLengths}))));
  await application.evaluate(({dialog}, directory) => { dialog.showOpenDialog = async () => ({canceled: false, filePaths: [directory]}); }, workspace);
  await window.locator('#workspace').click();
  await window.locator('#new-session').click();
  await window.locator('#message').waitFor({state: 'visible'});
  await window.locator('#message').fill('Write the desktop acceptance file in this workspace.');
  await window.locator('#send').click();
  await window.getByText('The workspace file is saved.', {exact: true}).waitFor({timeout: 30000});
  await window.locator('#stop').waitFor({state: 'hidden', timeout: 10000});
  assert.equal(await fs.readFile(path.join(workspace, 'desktop-acceptance.txt'), 'utf8'), 'native desktop verified\n');
  assert.ok(calls >= 2);
  assert.ok(!(await fs.readFile(path.join(home, 'settings.json'), 'utf8')).includes('desktop-acceptance-key'));
  assert.ok(!(await fs.readFile(path.join(home, 'backend/config.toml'), 'utf8')).includes('desktop-acceptance-key'));
  const state = await window.evaluate(() => window.overkill.state());
  assert.equal(JSON.stringify(state).includes('desktop-acceptance-key'), false);
  assert.equal('token' in state, false);
  await window.screenshot({path: path.resolve(__dirname, '../../.cache/desktop-chat-acceptance.png')});
  await window.locator('#message').fill('Wait until I stop this response.');
  await window.locator('#send').click();
  for (let attempt = 0; !hanging && attempt < 100; attempt++) await new Promise(resolve => setTimeout(resolve, 25));
  assert.equal(hanging, true, 'real provider stream started');
  await window.locator('#stop').click();
  await window.locator('#stop').waitFor({state: 'hidden', timeout: 10000});
  assert.equal(await window.locator('#message').isEnabled(), true);
  await window.waitForFunction(() => document.getElementById('activity').textContent === 'Ready when you are.');
  for (let attempt = 0; !cancelled && attempt < 100; attempt++) await new Promise(resolve => setTimeout(resolve, 25));
  assert.equal(cancelled, true, 'stop cancelled the provider transport');
  // Real authenticated IPC, persisted registrations and automatic local delivery.
  const sessionsBefore = await window.evaluate(() => window.overkill.listSessions());
  frontendID = sessionsBefore.sessions[0].id;
  await window.locator('#agent-name').fill('frontend');
  await window.locator('#agent-role').fill('Build frontend screens');
  await window.locator('#agent-register').click();
  await window.locator('#agent-identity').filter({hasText: 'frontend'}).waitFor();
  await window.locator('#new-session').click();
  await window.locator('#agent-name').fill('frontend');
  await window.locator('#agent-role').fill('Build backend endpoints');
  await window.locator('#agent-register').click();
  await window.locator('#agents-error').waitFor();
  assert.equal(await window.locator('#agent-identity').isVisible(), false, 'failed registration must not create a local identity');
  await window.locator('#agent-name').fill('backend');
  await window.locator('#agent-role').fill('Build backend endpoints');
  await window.locator('#agent-register').click();
  await window.locator('#agent-identity').filter({hasText: 'backend'}).waitFor();
  await window.locator('#agent-peers').getByText('frontend', {exact: true}).waitFor();
  await window.locator(`#sessions button[data-session-id="${frontendID}"]`).click();
  await window.locator('#agent-identity').filter({hasText: 'frontend'}).waitFor();
  const historicalTool = window.locator('#transcript details.tool').first();
  await historicalTool.waitFor();
  assert.equal(await historicalTool.getAttribute('open'), null, 'historical tool output starts collapsed');
  await historicalTool.locator(':scope > summary').click();
  await historicalTool.locator(':scope > pre').filter({hasText: 'wrote desktop-acceptance.txt'}).waitFor();
  await historicalTool.locator('details summary').click();
  await historicalTool.locator('details pre').filter({hasText: '"success":true'}).waitFor();
  await historicalTool.locator(':scope > summary').click();
  await window.locator('#peer-recipient').selectOption('backend');
  // Snapshot the intentionally invalid target and submit in one browser task;
  // background peer polling must not remove the fixture option before send.
  await window.evaluate(() => {
    const option = document.createElement('option'); option.value = 'missing-peer'; option.textContent = 'missing-peer';
    const recipients = document.getElementById('peer-recipient'); recipients.append(option); recipients.value = option.value;
    document.getElementById('peer-message').value = 'Which backend endpoint is ready?';
    document.getElementById('peer-composer').requestSubmit();
  });
  await window.locator('#agents-error').waitFor();
  const failedSend = await window.locator('#agents-error').textContent();
  await window.locator('#agents-refresh').click();
  await window.waitForFunction(() => !document.getElementById('agents-refresh').disabled);
  assert.equal(await window.locator('#agents-error').textContent(), failedSend, 'successful polling preserves an action failure until a successful retry');
  assert.equal(await window.locator('#peer-message').inputValue(), 'Which backend endpoint is ready?', 'failed send retains draft');
  assert.equal(await window.locator('#peer-messages .peer-message').count(), 0, 'failed send must not fabricate correspondence');
  await window.locator('#peer-recipient').selectOption('backend');
  await window.locator('#peer-send').click();
  await window.locator('#peer-messages').getByText('Which backend endpoint is ready?', {exact: true}).waitFor();
  await window.locator('#peer-messages .peer-message[data-state="started"]').first().waitFor({timeout: 15000});
  assert.equal(typeof releaseCoordination, 'function');
  releaseCoordination();
  await window.locator('#peer-messages').getByText('Backend update: the local endpoint is ready.', {exact: false}).waitFor({timeout: 30000});
  await window.locator('#peer-messages .peer-message[data-state="completed"]').first().waitFor();
  assert.equal(coordinationCalls, 1, 'request must execute the recipient through the real provider exactly once');
  await assert.rejects(window.evaluate(({sessionID}) => window.overkill.sendPeer({sessionID, to: 'backend', message: 'forged', key: 'bad', from: 'backend'}), {sessionID: frontendID}), /Invalid coordination request/);
  await window.screenshot({path: path.resolve(__dirname, '../../.cache/desktop-agents-acceptance.png')});
  await window.locator('#agents-panel').evaluate(panel => { panel.scrollTop = panel.scrollHeight; });
  await window.screenshot({path: path.resolve(__dirname, '../../.cache/desktop-agents-correspondence.png')});
  await application.evaluate(({BrowserWindow}) => BrowserWindow.getAllWindows()[0].setSize(860, 700));
  await window.locator('#agents-toggle').click();
  await window.locator('#agents-panel').waitFor({state: 'hidden'});
  await window.locator('#agents-toggle').click();
  await window.locator('#agents-panel').waitFor({state: 'visible'});
  await window.locator('#agents-panel').evaluate(panel => { panel.scrollTop = 0; });
  await window.screenshot({path: path.resolve(__dirname, '../../.cache/desktop-agents-narrow.png')});
  await application.evaluate(({BrowserWindow}) => BrowserWindow.getAllWindows()[0].setSize(1180, 820));


  await closeApplication();
  await assert.rejects(fs.stat(path.join(home, 'backend/postgres/postmaster.pid')), {code: 'ENOENT'});
  if (installedExecutable && process.env.OVERKILL_TEST_REINSTALL_SOURCE) {
    const appPath = path.resolve(installedExecutable, '../../..');
    const replacement = appPath + '.replacement'; const previous = appPath + '.previous';
    await fs.cp(process.env.OVERKILL_TEST_REINSTALL_SOURCE, replacement, {recursive: true, verbatimSymlinks: true});
    await fs.rename(appPath, previous);
    try { await fs.rename(replacement, appPath); }
    catch (error) { await fs.rename(previous, appPath); throw error; }
    await promisify(execFile)('/usr/bin/codesign', ['--verify', '--deep', '--strict', appPath]);
    await fs.rm(previous, {recursive: true});
    t.diagnostic('Replaced the installed application from the mounted DMG before relaunch; same-version data preservation only.');
  }
  application = await launch(); window = await application.firstWindow();
  if (mockStorageSmoke) {
    await window.locator('dialog[open]').waitFor();
    await window.locator('#model').fill('desktop-fixture-model');
    await window.locator('#base-url').fill(`http://127.0.0.1:${fixture.address().port}/v1`);
    // Leave API key blank: saving must reuse the restored fake credential.
    assert.equal(await window.locator('#api-key').inputValue(), '');
    await window.locator('#settings-save').click();
    await window.locator('dialog[open]').waitFor({state: 'hidden', timeout: 45000});
    const calls = await application.evaluate(() => globalThis.installedMockStorageSmoke);
    assert.ok(calls.decrypt > 0, 'reconnect reused the persisted fake credential');
    t.diagnostic('Smoke relaunch restored persisted data through explicit reconnect; automatic Keychain startup remains untested.');
  }
  await window.locator('#sessions button').first().waitFor({timeout: 45000});
  await window.locator(`#sessions button[data-session-id="${frontendID}"]`).click();
  await window.getByText('The workspace file is saved.', {exact: true}).waitFor();
  await window.locator('#transcript').getByText('Write the desktop acceptance file in this workspace.', {exact: true}).waitFor();
  assert.equal(await window.locator('dialog[open]').count(), 0);
  await window.locator('#agent-identity').filter({hasText: 'frontend'}).waitFor();
  await window.locator('#peer-messages').getByText('Backend update: the local endpoint is ready.', {exact: false}).waitFor();
  // A real owned-database crash must take the API offline, report recovery in
  // the window, and preserve the same committed conversation on reconnect.
  const owner = JSON.parse(await fs.readFile(path.join(home, 'backend/postgres.manager.json'), 'utf8'));
  assert.ok(Number.isInteger(owner.pid) && owner.pid > 1);
  process.kill(owner.pid, 'SIGKILL');
  await window.locator('#error').filter({hasText: 'The backend stopped.'}).waitFor({timeout: 15000});
  assert.equal((await window.evaluate(() => window.overkill.state())).ready, false);
  await window.locator('#settings-button').click();
  await window.locator('#settings-save').click();
  await window.locator('dialog[open]').waitFor({state: 'hidden', timeout: 45000});
  assert.equal((await window.evaluate(() => window.overkill.state())).ready, true);
  await window.locator(`#sessions button[data-session-id="${frontendID}"]`).click();
  await window.getByText('The workspace file is saved.', {exact: true}).waitFor();
  await closeApplication();
  await assert.rejects(fs.stat(path.join(home, 'backend/postgres/postmaster.pid')), {code: 'ENOENT'});
});
