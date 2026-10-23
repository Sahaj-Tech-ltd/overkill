const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const {_electron} = require('playwright');

test('a blocked Keychain leaves a responsive native window and actionable timeout', {skip: process.env.OVERKILL_REQUIRE_DESKTOP_TEST !== '1', timeout: 45000}, async t => {
  const home = await fs.mkdtemp(path.join(os.tmpdir(), 'overkill-window-keychain-'));
  await fs.writeFile(path.join(home, 'settings.json'), JSON.stringify({version: 1, settings: {provider: 'openai', model: 'blocked-fixture', baseURL: ''}, credential: Buffer.from('fixture-not-a-real-key').toString('base64')}));
  let app;
  t.after(async () => { if (app) await app.close(); await fs.rm(home, {recursive: true, force: true}); });
  app = await _electron.launch({args: [path.join(__dirname, 'fixtures/blocked-keychain.cjs'), `--overkill-profile=${home}`], timeout: 15000});
  const window = await app.firstWindow({timeout: 5000});
  const state = await window.evaluate(() => window.overkill.state());
  assert.equal(state.ready, false); assert.equal(state.starting, true);
  assert.equal(state.settings.model, 'blocked-fixture');
  await window.locator('#error').filter({hasText: 'Keychain did not respond.'}).waitFor({timeout: 20000});
  await window.locator('dialog[open]').waitFor();
  assert.equal((await window.evaluate(() => window.overkill.state())).starting, false);
  await app.evaluate(() => globalThis.releaseKeychainFixture(true));
  assert.equal((await window.evaluate(() => window.overkill.state())).ready, false, 'late Keychain response cannot start a backend after timeout');
});
