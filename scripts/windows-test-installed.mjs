#!/usr/bin/env node
// Native Windows only. Runs the installed executable; no resource, encryption,
// backend or application-source replacement is used by this acceptance gate.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import http from 'node:http';
import {once} from 'node:events';
import {createHash} from 'node:crypto';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(path.join(root, 'desktop/package.json'));
const {_electron} = require('playwright');
const exec = promisify(execFile);
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
async function missingSoon(file) {
  for (let attempt = 0; attempt < 100; attempt++) {
    try { await fs.access(file); } catch (error) { if (error.code === 'ENOENT') return; throw error; }
    await sleep(200);
  }
  throw new Error(`Owned process/file remained after graceful app close: ${file}`);
}
async function main() {
  if (process.platform !== 'win32' || process.arch !== 'x64' || process.argv.length !== 3) throw new Error('Usage on native Windows x64: node scripts/windows-test-installed.mjs /absolute/path/to/setup.exe');
  const installer = path.resolve(process.argv[2]);
  const output = path.dirname(installer);
  const artifact = JSON.parse(await fs.readFile(path.join(output, 'artifact.json'), 'utf8'));
  assert.equal(hash(await fs.readFile(installer)), artifact.installerSHA256);
  if (process.env.GITHUB_SHA) assert.equal(artifact.sourceCommit, process.env.GITHUB_SHA);
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'Overkill Windows Acceptance '));
  const install = path.join(temp, 'Installed App With Spaces');
  const profile = path.join(temp, 'Private Profile With Spaces');
  const workspace = path.join(temp, 'Selected Workspace With Spaces');
  await fs.mkdir(workspace);
  const source = "export const value = 'WINDOWS_ACTUAL_SOURCE_EVIDENCE_946102';\n";
  await fs.writeFile(path.join(workspace, 'evidence.ts'), source);
  const fakeKey = 'windows-controlled-fake-provider-key';
  let application, providerError, calls = 0, uninstalled = false;
  const pageErrors = [];
  const fixture = http.createServer(async (request, response) => {
    try {
      assert.equal(request.url, '/v1/chat/completions');
      assert.equal(request.headers.authorization, `Bearer ${fakeKey}`);
      let bytes = ''; for await (const chunk of request) { bytes += chunk; assert.ok(bytes.length < 2 * 1024 * 1024); }
      const body = JSON.parse(bytes);
      assert.equal(body.model, 'windows-native-fixture');
      assert.ok(JSON.stringify(body).includes(source.trim()), 'actual installed provider request must include source file bytes');
      calls++;
      const content = 'Windows installed workspace evidence received.';
      const usage = {prompt_tokens: 20, completion_tokens: 6};
      if (body.stream) {
        response.writeHead(200, {'Content-Type': 'text/event-stream'});
        response.end(`data: ${JSON.stringify({choices: [{index: 0, delta: {content}, finish_reason: 'stop'}], usage})}\n\ndata: [DONE]\n\n`);
      } else {
        response.writeHead(200, {'Content-Type': 'application/json'});
        response.end(JSON.stringify({choices: [{message: {role: 'assistant', content}, finish_reason: 'stop'}], usage}));
      }
    } catch (error) { providerError = error; response.writeHead(500); response.end('Controlled acceptance failed.'); }
  });
  fixture.listen(0, '127.0.0.1'); await once(fixture, 'listening');
  const executablePath = path.join(install, 'Overkill.exe');
  const uninstaller = path.join(install, 'unins000.exe');
  async function launch() {
    const env = {...process.env}; delete env.ELECTRON_RUN_AS_NODE;
    delete env.OVERKILL_DESKTOP_RESOURCES; delete env.OVERKILL_DESKTOP_DATA_DIR;
    application = await _electron.launch({executablePath, args: [`--overkill-profile=${profile}`], env, timeout: 45000});
    const page = await application.firstWindow();
    page.on('pageerror', error => pageErrors.push(error.message));
    assert.equal(await application.evaluate(({app}) => app.isPackaged), true);
    assert.equal(await application.evaluate(() => process.platform), 'win32');
    return page;
  }
  try {
    await exec(installer, ['/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', `/DIR=${install}`, `/LOG=${path.join(output, 'installer.log')}`], {windowsHide: true, timeout: 180000});
    const manifest = JSON.parse(await fs.readFile(path.join(install, 'resources/resource-manifest.json'), 'utf8'));
    assert.equal(manifest.sourceCommit, artifact.sourceCommit);
    assert.equal(hash(await fs.readFile(path.join(install, 'resources/bin/overkill.exe'))), manifest.backendSHA256);
    let page = await launch();
    // Call the actual Electron Windows encryption implementation in the
    // installed process. Never monkeypatch safeStorage to pass this gate.
    const encryption = await application.evaluate(async ({safeStorage}) => {
      const available = await safeStorage.isAsyncEncryptionAvailable();
      if (!available) throw new Error('Windows native safeStorage unavailable.');
      const value = 'disposable-windows-encryption-probe';
      const encrypted = await safeStorage.encryptStringAsync(value);
      const decrypted = await safeStorage.decryptStringAsync(encrypted);
      return {available, correct: decrypted.result === value, ciphertextDiffers: !encrypted.equals(Buffer.from(value)), bytes: encrypted.length};
    });
    assert.ok(encryption.available && encryption.correct && encryption.ciphertextDiffers);
    await page.locator('#settings[open]').waitFor();
    await page.locator('#provider').selectOption('custom');
    await page.locator('#model').fill('windows-native-fixture');
    await page.locator('#base-url').fill(`http://127.0.0.1:${fixture.address().port}/v1`);
    await page.locator('#api-key').fill(fakeKey);
    await page.locator('#settings-save').click();
    try { await page.locator('#settings[open]').waitFor({state: 'hidden', timeout: 60000}); }
    catch (error) { throw new Error(`${error.message}; UI error: ${await page.locator('#error').textContent()}`); }
    const docBytes = await fs.readFile(path.join(profile, 'settings.json'), 'utf8');
    assert.ok(!docBytes.includes(fakeKey), 'saved provider key must be encrypted');
    const doc = JSON.parse(docBytes); assert.ok(doc.credential.length > 20);
    await application.evaluate(({dialog}, selected) => { dialog.showOpenDialog = async () => ({canceled: false, filePaths: [selected]}); }, workspace);
    await page.locator('#workspace').click(); await page.locator('#new-session').click();
    await page.locator('#message').fill('Inspect @evidence.ts from this workspace.'); await page.locator('#send').click();
    try {
      await page.locator('#transcript').getByText('Windows installed workspace evidence received.', {exact: false}).last().waitFor({timeout: 30000});
      await page.locator('#stop').waitFor({state: 'hidden', timeout: 30000});
    } catch (error) { throw providerError || error; }
    assert.equal(calls, 1);
    const sessions = await page.evaluate(() => window.overkill.listSessions());
    const session = sessions.sessions.find(item => item.folder.toLowerCase() === workspace.toLowerCase());
    assert.ok(session);
    const before = await page.evaluate(id => window.overkill.messages(id), session.id);
    await page.screenshot({path: path.join(output, 'windows-installed-before-restart.png')});
    await application.close(); application = null;
    await missingSoon(path.join(profile, 'backend/postgres/postmaster.pid'));
    // Exercise the same installer over its installed destination. This tests
    // same-version replacement, not a cross-version migration promise.
    await exec(installer, ['/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', `/DIR=${install}`], {windowsHide: true, timeout: 180000});
    page = await launch();
    await page.waitForFunction(() => window.overkill.state().then(state => state.ready), null, {timeout: 60000});
    await page.locator(`#sessions button[data-session-id="${session.id}"]`).click();
    await page.locator('#transcript').getByText('Windows installed workspace evidence received.', {exact: false}).last().waitFor({timeout: 10000});
    const after = await page.evaluate(id => window.overkill.messages(id), session.id);
    assert.deepEqual(after, before, 'installed relaunch must preserve the session transcript');
    assert.equal(calls, 1, 'restart must not replay completed provider work');
    assert.equal(await fs.readFile(path.join(workspace, 'evidence.ts'), 'utf8'), source);
    assert.deepEqual(pageErrors, []);
    await page.screenshot({path: path.join(output, 'windows-installed-after-restart.png')});
    await application.close(); application = null;
    await missingSoon(path.join(profile, 'backend/postgres/postmaster.pid'));
    await exec(uninstaller, ['/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', `/LOG=${path.join(output, 'uninstaller.log')}`], {windowsHide: true, timeout: 120000});
    await missingSoon(executablePath); uninstalled = true;
    // Uninstall intentionally preserves user data. Only this acceptance script
    // later removes its own temporary root.
    await fs.access(path.join(profile, 'settings.json'));
    const report = {schema: 1, passed: true, completedAt: new Date().toISOString(), sourceCommit: artifact.sourceCommit, installer: path.basename(installer), installerSHA256: artifact.installerSHA256, host: {platform: process.platform, architecture: process.arch, release: os.release()}, credentialStorage: 'actual installed Electron45 Windows safeStorage; native encrypted roundtrip and automatic relaunch', encryption, providerCalls: calls, cases: ['silent native installer into path with spaces', 'manifest-bound installed backend', 'actual Windows safeStorage with encrypted saved key', 'bundled private PostgreSQL startup/migrations/shutdown', 'actual workspace source in provider HTTP request', 'renderer streamed response', 'same-version replacement', 'automatic encrypted settings startup and persisted history', 'no replay after restart', 'silent uninstall and intentional user-data retention'], limitations: ['controlled local provider; no paid provider', 'unsigned Authenticode; SmartScreen reputation not tested', 'GitHub runner Windows only; minimum Windows version not executed', 'same-version replacement; no cross-version migration', 'native dependency/source redistribution review separate'], distributionReady: false};
    await fs.writeFile(path.join(output, 'windows-installed-acceptance.json'), JSON.stringify(report, null, 2) + '\n');
    artifact.installedAcceptance = 'passed: windows-installed-acceptance.json';
    await fs.writeFile(path.join(output, 'artifact.json'), JSON.stringify(artifact, null, 2) + '\n');
    console.log(JSON.stringify(report, null, 2));
  } finally {
    if (application) await application.close();
    fixture.closeAllConnections(); await new Promise(resolve => fixture.close(resolve));
    if (!uninstalled) {
      try { await exec(uninstaller, ['/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART'], {windowsHide: true, timeout: 120000}); } catch {}
    }
    await fs.rm(temp, {recursive: true, force: true, maxRetries: 10, retryDelay: 500});
  }
}
main().catch(error => { console.error(error); process.exitCode = 1; });
