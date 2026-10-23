const fs = require('node:fs/promises');
const path = require('node:path');
const crypto = require('node:crypto');
const credentialStore = process.platform === 'darwin' ? 'Keychain' : 'Credential store';
const credentialAdvice = process.platform === 'darwin' ? 'Unlock or authorize access in macOS' : 'Unlock or authorize access in your operating system';

const types = new Set(['openai', 'anthropic', 'custom']);
function validateSettings(input) {
  if (!input || typeof input !== 'object' || Array.isArray(input)) throw new Error('Settings must be an object.');
  for (const key of Object.keys(input)) if (!['provider', 'model', 'baseURL', 'apiKey', 'workspaceInstructions', 'autoMode'].includes(key)) throw new Error('Unknown setting.');
  if (!types.has(input.provider)) throw new Error('Choose a supported provider.');
  if (typeof input.model !== 'string' || !/^[\w./:@+-]{1,200}$/.test(input.model)) throw new Error('Enter a valid model ID.');
  const baseURL = input.baseURL || '';
  if (typeof baseURL !== 'string' || baseURL.length > 2048) throw new Error('Invalid provider URL.');
  if (baseURL) {
    const url = new URL(baseURL);
    if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) throw new Error('Use an HTTP(S) URL without credentials, query or fragment.');
    if (url.protocol === 'http:' && !['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname)) throw new Error('Remote providers require HTTPS.');
  }
  if (input.provider === 'custom' && !baseURL) throw new Error('Enter the compatible provider URL.');
  if (input.apiKey !== undefined && (typeof input.apiKey !== 'string' || input.apiKey.length > 8192 || /[\r\n\0]/.test(input.apiKey))) throw new Error('Invalid API key.');
  for(const key of ['workspaceInstructions','autoMode'])if(input[key]!==undefined && typeof input[key]!=='boolean')throw new Error('Invalid workspace preference.');
  return {provider: input.provider, model: input.model, baseURL, workspaceInstructions: input.workspaceInstructions ?? false, autoMode: input.autoMode ?? false};
}

async function atomicWrite(file, data) {
  await fs.mkdir(path.dirname(file), {recursive: true, mode: 0o700});
  const temporary = `${file}.${crypto.randomUUID()}.tmp`;
  try {
    const handle = await fs.open(temporary, 'wx', 0o600);
    try { await handle.writeFile(data); await handle.sync(); } finally { await handle.close(); }
    await fs.rename(temporary, file);
  } finally { await fs.rm(temporary, {force: true}); }
}

// Only the main process receives a decrypted key. A single atomic document
// keeps settings and their encrypted credential in the same revision.
class SettingsStore {
  constructor(directory, encryption, {keychainTimeoutMs = 15000} = {}) { this.file = path.join(directory, 'settings.json'); this.encryption = encryption; this.keychainTimeoutMs = keychainTimeoutMs; this.pending = Promise.resolve(); }
  async keychain(method, ...args) {
    let timer;
    try {
      return await Promise.race([
        Promise.resolve().then(() => this.encryption[method](...args)),
        new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(`${credentialStore} did not respond. ${credentialAdvice}, then reconnect.`)), this.keychainTimeoutMs); }),
      ]);
    } finally { clearTimeout(timer); }
  }
  serial(operation) {
    const result = this.pending.then(operation);
    this.pending = result.catch(() => {});
    return result;
  }
  read() { return this.serial(() => this.load()); }
  async document() {
    let doc;
    try { doc = JSON.parse(await fs.readFile(this.file, 'utf8')); }
    catch (error) { if (error.code === 'ENOENT') return null; throw new Error('Saved settings could not be read.'); }
    if (doc.version !== 1 || typeof doc.credential !== 'string') throw new Error('Unsupported settings format.');
    validateSettings(doc.settings);
    return doc;
  }
  async load() {
    const doc = await this.document();
    if (!doc) return null;
    const settings = validateSettings(doc.settings);
    if (!await this.keychain('isAsyncEncryptionAvailable')) throw new Error(`Unlock the ${credentialStore} to access the provider key.`);
    const {result: apiKey, shouldReEncrypt} = await this.keychain('decryptStringAsync', Buffer.from(doc.credential, 'base64'));
    if (shouldReEncrypt) {
      doc.credential = (await this.keychain('encryptStringAsync', apiKey)).toString('base64');
      await atomicWrite(this.file, JSON.stringify(doc));
    }
    return {...settings, apiKey};
  }
  save(input) { return this.serial(() => this.persist(input)); }
  async persist(input) {
    const settings = validateSettings(input);
    const previous = input.apiKey ? null : await this.load();
    const apiKey = input.apiKey || (previous?.provider === settings.provider && previous.baseURL === settings.baseURL ? previous.apiKey : '');
    if (!apiKey) throw new Error('Enter a provider API key.');
    if (!await this.keychain('isAsyncEncryptionAvailable')) throw new Error(`Unlock the ${credentialStore} before saving credentials.`);
    const credential = (await this.keychain('encryptStringAsync', apiKey)).toString('base64');
    await atomicWrite(this.file, JSON.stringify({version: 1, settings, credential}));
    return settings;
  }
  async publicSettings() { const doc = await this.document(); return doc && {...validateSettings(doc.settings), hasKey: !!doc.credential}; }
}

function configTOML(settings, backendHome) {
  // JSON strings are a subset of TOML basic strings for these validated values.
  const quote = JSON.stringify;
  return `version = 1\n[agent]\ndefault_provider = "desktop"\ndefault_model = ${quote(settings.model)}\nmax_turns = 40\n[agent.instructions]\nenabled = ${settings.workspaceInstructions === true}\n[[providers]]\nname = "desktop"\ntype = ${quote(settings.provider)}\napi_key = "\u0024{OVERKILL_DESKTOP_PROVIDER_KEY}"\nbase_url = ${quote(settings.baseURL)}\n[[providers.models]]\nid = ${quote(settings.model)}\nname = ${quote(settings.model)}\n[session]\ndata_dir = ${quote(path.join(backendHome, 'sessions'))}\n[security]\nautonomy_level = ${quote(settings.autoMode === true ? "auto" : "supervised")}\nsandbox_enabled = false\n`;
}
module.exports = {SettingsStore, validateSettings, atomicWrite, configTOML};
