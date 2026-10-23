const {spawn} = require('node:child_process');
const path = require('node:path');
const crypto = require('node:crypto');
const {atomicWrite, configTOML} = require('./settings.cjs');

function validateReady(value) {
  if (value?.type !== 'ready' || value.protocol !== 1 || typeof value.token !== 'string' || !/^[a-zA-Z0-9_-]{32,256}$/.test(value.token)) throw new Error('Backend returned invalid startup credentials.');
  const url = new URL(value.address);
  if (url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || !url.port || url.username || url.password || url.pathname !== '/' || url.search || url.hash) throw new Error('Backend did not bind to a private loopback address.');
  return {address: url.origin, token: value.token};
}

async function boundedJSON(response, maxBytes = 4 * 1024 * 1024) {
  if (!response.ok) throw new Error(`Backend HTTP ${response.status}.`);
  let length = 0;
  const chunks = [];
  for await (const chunk of response.body) {
    length += chunk.length;
    if (length > maxBytes) throw new Error('Backend response exceeded the desktop limit.');
    chunks.push(chunk);
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}

// Decodes UTF-8 split across chunks; rejects malformed/oversized events rather
// than silently dropping them and presenting a truncated answer as complete.
async function* parseSSE(body) {
  const decoder = new TextDecoder('utf-8', {fatal: true});
  let buffer = '', event = '', data = [];
  for await (const chunk of body) {
    buffer += decoder.decode(chunk, {stream: true});
    if (buffer.length > 2 * 1024 * 1024) throw new Error('Backend stream event is too large.');
    let index;
    while ((index = buffer.indexOf('\n')) >= 0) {
      const line = buffer.slice(0, index).replace(/\r$/, ''); buffer = buffer.slice(index + 1);
      if (line === '') {
        if (data.length) {
          const payload = JSON.parse(data.join('\n'));
          yield {type: event || 'message', data: payload};
        }
        event = ''; data = [];
      } else if (line.startsWith('event:')) event = line.slice(6).trim();
      else if (line.startsWith('data:')) {
        data.push(line.slice(5).replace(/^ /, ''));
        if (data.reduce((n, s) => n + s.length, 0) > 2 * 1024 * 1024) throw new Error('Backend stream event is too large.');
      }
    }
  }
  buffer += decoder.decode();
  if (buffer.trim() || data.length) throw new Error('Backend stream ended during an event.');
}

class Backend {
  constructor({executable, postgresBin, home, onExit = () => {}, spawnProcess = spawn, writeConfig = atomicWrite}) {
    Object.assign(this, {executable, postgresBin, home, onExit, spawnProcess, writeConfig});
    this.epoch = 0;
    this.child = null; this.credentials = null; this.streams = new Map(); this.stopping = false;
  }
  async start(settings) {
    if (this.child) throw new Error('Backend is already running.');
    const epoch = ++this.epoch;
    await this.writeConfig(path.join(this.home, 'config.toml'), configTOML(settings, this.home));
    if (epoch !== this.epoch) throw new Error('Backend startup was cancelled.');
    this.stopping = false;
    this.secret = settings.apiKey;
    // Inherit only basic OS and locale values. No accidental shell/provider
    // secrets, bridge settings, database URLs or test switches reach the app.
    const env = {};
    const inherited = ['HOME', 'USER', 'LOGNAME', 'TMPDIR', 'LANG', 'LC_ALL', 'PATH'];
    if (process.platform === 'win32') inherited.push('SystemRoot', 'WINDIR', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA', 'TEMP', 'TMP', 'USERNAME', 'PATHEXT');
    for (const key of inherited) if (process.env[key]) env[key] = process.env[key];
    Object.assign(env, {OVERKILL_HOME: this.home, OVERKILL_DESKTOP_PROVIDER_KEY: settings.apiKey});
    const child = this.spawnProcess(this.executable, ['desktop-server', '--postgres-bin', this.postgresBin, '--data-dir', path.join(this.home, 'postgres')], {env, cwd: this.home, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true});
    child.stdin.on('error', () => {}); // Close is the authoritative child outcome.
    this.child = child;
    this.exited = new Promise(resolve => child.once('close', (code, signal) => {
      this.credentials = null; this.child = null;
      for (const controller of this.streams.values()) controller.abort();
      this.streams.clear();
      if (!this.stopping) this.onExit({code, signal});
      resolve();
    }));
    // Drain stderr without persisting model text, provider errors or credentials.
    child.stderr.resume();
    try {
      this.credentials = await new Promise((resolve, reject) => {
        let buffer = '';
        const timeout = setTimeout(() => finish(new Error('Backend startup timed out.')), 60000);
        const finish = (error, value) => {
          clearTimeout(timeout); child.stdout.off('data', read); child.off('error', failed); child.off('close', earlyExit);
          error ? reject(error) : resolve(value);
        };
        const failed = () => finish(new Error('The bundled backend could not be started.'));
        const earlyExit = () => finish(new Error('Backend exited before becoming ready.'));
        const read = chunk => {
          buffer += chunk.toString('utf8');
          if (buffer.length > 16384) return finish(new Error('Invalid backend startup response.'));
          const newline = buffer.indexOf('\n');
          if (newline < 0) return;
          try { finish(null, validateReady(JSON.parse(buffer.slice(0, newline)))); }
          catch { finish(new Error('Invalid backend startup response.')); }
        };
        child.once('error', failed); child.once('close', earlyExit); child.stdout.on('data', read);
      });
      child.stdout.resume();
      const health = await fetch(`${this.credentials.address}/health`, {headers: this.headers(), signal: AbortSignal.timeout(5000)});
      if (!health.ok) throw new Error('Backend readiness check failed.');
      if (epoch !== this.epoch) throw new Error('Backend startup was cancelled.');
    } catch (error) { await this.stop(); throw error; }
  }
  headers() {
    if (!this.credentials) throw new Error('Backend is not ready.');
    return {Authorization: `Bearer ${this.credentials.token}`, 'Content-Type': 'application/json'};
  }
  publicValue(value) {
    const secrets = [this.secret, this.credentials?.token].filter(Boolean);
    return JSON.parse(JSON.stringify(value, (_key, item) => typeof item === 'string' ? secrets.reduce((text, secret) => text.split(secret).join('[redacted]'), item) : item));
  }
  async call(method, params) {
    const headers = this.headers();
    const result = await boundedJSON(await fetch(`${this.credentials.address}/rpc`, {method: 'POST', headers, body: JSON.stringify({jsonrpc: '2.0', id: crypto.randomUUID(), method, params}), signal: AbortSignal.timeout(30000)}));
    if (result.error) throw new Error(this.publicValue(result.error.message || 'Backend operation failed.'));
    return this.publicValue(result.result);
  }
  startStream(id, sessionID, message, emit, onAdmission = () => {}) {
    if (this.streams.size) throw new Error('A response is already running.');
    if (!this.credentials) throw new Error('Backend is not ready.');
    const controller = new AbortController(); this.streams.set(id, controller);
    let accept, reject;
    const admission = new Promise((resolve, fail) => { accept = resolve; reject = fail; });
    const completion = (async () => {
      let terminal = false, admitted = false, timedOut = false;
      const timeout = setTimeout(() => { timedOut = true; controller.abort(); }, 30000); timeout.unref();
      try {
        const url = new URL(`${this.credentials.address}/stream`);
        url.searchParams.set('method', 'agent.send');
        url.searchParams.set('params', JSON.stringify({session_id: sessionID, message}));
        const response = await fetch(url, {headers: this.headers(), signal: controller.signal});
        if (!response.ok || !response.headers.get('content-type')?.startsWith('text/event-stream')) {
          await response.body?.cancel();
          throw new Error(`Backend stream failed (${response.status}).`);
        }
        if (controller.signal.aborted) throw new Error('Response stopped before admission.');
        clearTimeout(timeout);
        // Notify before reading any event, including an immediate done/error.
        // Headers admit a turn; they do not attest persistence or completion.
        onAdmission(); admitted = true; accept({id, accepted: true});
        for await (const event of parseSSE(response.body)) {
          if (!['text', 'tool_call', 'status', 'reasoning', 'error', 'done'].includes(event.type)) continue;
          if (terminal) throw new Error('Backend sent data after completion.');
          if (['done', 'error'].includes(event.type)) terminal = true;
          emit(this.publicValue({id, ...event}));
        }
        if (!terminal && !controller.signal.aborted) throw new Error('Backend disconnected before saving the response.');
      } catch (error) {
        const message = timedOut ? 'Backend did not admit the response in time.' : controller.signal.aborted ? 'Response stopped.' : error.message;
        if (!admitted) reject(new Error(this.publicValue(message)));
        else if (!terminal) emit(this.publicValue({id, type: 'error', data: {message}}));
      } finally { clearTimeout(timeout); this.streams.delete(id); }
    })();
    return {admission, completion};
  }
  async stream(id, sessionID, message, emit) {
    const started = this.startStream(id, sessionID, message, emit);
    try { await started.admission; }
    catch (error) { emit(this.publicValue({id, type: 'error', data: {message: error.message}})); }
    await started.completion;
  }
  stop() {
    this.epoch++;
    if (!this.stoppingPromise) this.stoppingPromise = this.stopOwned().finally(() => { this.stoppingPromise = null; });
    return this.stoppingPromise;
  }
  async stopOwned() {
    this.stopping = true;
    for (const controller of this.streams.values()) controller.abort();
    const child = this.child;
    if (!child) { this.secret = ''; return; }
    child.stdin.end();
    const graceful = await Promise.race([this.exited.then(() => true), new Promise(resolve => { const t = setTimeout(() => resolve(false), 15000); t.unref(); })]);
    if (!graceful) {
      child.kill('SIGTERM');
      const terminated = await Promise.race([this.exited.then(() => true), new Promise(resolve => { const t = setTimeout(() => resolve(false), 10000); t.unref(); })]);
      if (!terminated) { child.kill('SIGKILL'); await this.exited; }
    }
    this.credentials = null;
    this.secret = '';
  }
}
module.exports = {Backend, validateReady, boundedJSON, parseSSE};
