const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const {SettingsStore, validateSettings, configTOML} = require('../src/settings.cjs');
const encryption = {isAsyncEncryptionAvailable: async () => true, encryptStringAsync: async value => Buffer.from(`encrypted:${Buffer.from(value).toString('base64')}`), decryptStringAsync: async value => ({result: Buffer.from(value.toString().slice(10), 'base64').toString(), shouldReEncrypt: false})};
test('settings retain the encrypted key without exposing it in public settings or config', async t => {
  const home = await fs.mkdtemp(path.join(os.tmpdir(), 'overkill-settings-')); t.after(() => fs.rm(home, {recursive: true, force: true}));
  const store = new SettingsStore(home, encryption);
  assert.equal(await store.read(), null);
  await store.save({provider: 'openai', model: 'model-test', apiKey: 'private-test-key'});
  assert.equal((await store.read()).apiKey, 'private-test-key');
  assert.deepEqual(await store.publicSettings(), {provider: 'openai', model: 'model-test', baseURL: '', workspaceInstructions:false, autoMode:false, hasKey: true});
  const disk = await fs.readFile(path.join(home, 'settings.json'), 'utf8'); assert.ok(!disk.includes('private-test-key'));
  assert.equal((await fs.stat(path.join(home, 'settings.json'))).mode & 0o777, 0o600);
  await store.save({provider: 'openai', model: 'next-test'}); assert.equal((await store.read()).apiKey, 'private-test-key');
  await assert.rejects(store.save({provider: 'anthropic', model: 'next-test'}), /API key/);
  await assert.rejects(store.save({provider: 'openai', model: 'next-test', baseURL: 'https://another.example/v1'}), /API key/);
  const toml = configTOML(await store.read(), home); assert.ok(!toml.includes('private-test-key')); assert.ok(toml.includes('${OVERKILL_DESKTOP_PROVIDER_KEY}'));
});
test('no plaintext fallback when Keychain is unavailable', async t => {
  const home = await fs.mkdtemp(path.join(os.tmpdir(), 'overkill-settings-')); t.after(() => fs.rm(home, {recursive: true, force: true}));
  const store = new SettingsStore(home, {...encryption, isAsyncEncryptionAvailable: async () => false});
  await assert.rejects(store.save({provider: 'openai', model: 'test', apiKey: 'private-test'}), /Keychain/);
  await assert.rejects(fs.stat(path.join(home, 'settings.json')), {code: 'ENOENT'});
});
test('reject unsafe endpoints and configuration injection', () => {
  for (const baseURL of ['file:///etc/passwd', 'https://secret:pw@example.com', 'https://example.com/?token=secret', 'http://remote.example/v1', 'https://example.com/#fragment']) assert.throws(() => validateSettings({provider: 'custom', model: 'test', baseURL}));
  assert.throws(() => validateSettings({provider: 'openai', model: 'x"\n[security]'}));
  assert.throws(() => validateSettings({provider: 'openai', model: 'test', injected: true}));
  assert.equal(validateSettings({provider: 'custom', model: 'test', baseURL: 'http://127.0.0.1:9000/v1'}).model, 'test');
});

test('public settings stay available while Keychain access waits and times out', async t => {
  const home = await fs.mkdtemp(path.join(os.tmpdir(), 'overkill-locked-keychain-')); t.after(() => fs.rm(home, {recursive: true, force: true}));
  await new SettingsStore(home, encryption).save({provider: 'openai', model: 'saved-model', apiKey: 'fixture-key'});
  let release; let decryptCalls = 0;
  const blocked = new SettingsStore(home, {...encryption, isAsyncEncryptionAvailable: () => new Promise(resolve => { release = resolve; }), decryptStringAsync: () => { decryptCalls++; throw new Error('should not decrypt after timeout'); }}, {keychainTimeoutMs: 25});
  const rejected = assert.rejects(blocked.read(), /Keychain did not respond/);
  assert.deepEqual(await blocked.publicSettings(), {provider: 'openai', model: 'saved-model', baseURL: '', workspaceInstructions:false, autoMode:false, hasKey: true});
  await rejected;
  release(true); await new Promise(resolve => setImmediate(resolve));
  assert.equal(decryptCalls, 0);
});

test('late encryption after timeout never publishes new saved credentials', async t => {
  const home = await fs.mkdtemp(path.join(os.tmpdir(), 'overkill-keychain-timeout-')); t.after(() => fs.rm(home, {recursive: true, force: true}));
  let release;
  const blocked = new SettingsStore(home, {...encryption, encryptStringAsync: () => new Promise(resolve => { release = resolve; })}, {keychainTimeoutMs: 25});
  await assert.rejects(blocked.save({provider: 'openai', model: 'fixture', apiKey: 'fixture-key'}), /Keychain did not respond/);
  release(Buffer.from('late-encrypted-result')); await new Promise(resolve => setImmediate(resolve));
  await assert.rejects(fs.stat(path.join(home, 'settings.json')), {code: 'ENOENT'});
});

test('entering a replacement key recovers without decrypting an inaccessible old key', async t => {
  const home = await fs.mkdtemp(path.join(os.tmpdir(), 'overkill-key-replacement-')); t.after(() => fs.rm(home, {recursive: true, force: true}));
  await new SettingsStore(home, encryption).save({provider: 'openai', model: 'fixture', apiKey: 'old-fixture-key'});
  const replacement = new SettingsStore(home, {...encryption, decryptStringAsync: async () => { throw new Error('old key is inaccessible'); }});
  await replacement.save({provider: 'openai', model: 'fixture', apiKey: 'new-fixture-key'});
  assert.equal((await new SettingsStore(home, encryption).read()).apiKey, 'new-fixture-key');
});


test('workspace guidance and review are explicit opt-ins in saved settings and backend config', async t=>{
  const home=await fs.mkdtemp(path.join(os.tmpdir(),'overkill-preferences-'));t.after(()=>fs.rm(home,{recursive:true,force:true}));
  const store=new SettingsStore(home,encryption);
  const legacy=validateSettings({provider:'openai',model:'test'});assert.equal(legacy.workspaceInstructions,false);assert.equal(legacy.autoMode,false);
  await store.save({provider:'openai',model:'test',apiKey:'fixture-key',workspaceInstructions:true,autoMode:true});
  const saved=await store.read();assert.equal(saved.workspaceInstructions,true);assert.equal(saved.autoMode,true);
  const config=configTOML(saved,home);assert.match(config,/\[agent.instructions\]\nenabled = true/);assert.match(config,/autonomy_level = "auto"/);
  assert.equal((await store.publicSettings()).workspaceInstructions,true);assert.equal((await store.publicSettings()).apiKey,undefined);
  for(const field of ['workspaceInstructions','autoMode'])for(const value of ['true',1,null])assert.throws(()=>validateSettings({provider:'openai',model:'test',[field]:value}));
  assert.match(configTOML(legacy,home),/autonomy_level = "supervised"/);assert.match(configTOML(legacy,home),/enabled = false/);
});
