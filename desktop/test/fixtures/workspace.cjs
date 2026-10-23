// Disposable local acceptance only: fake credentials never use macOS Keychain.
// This fixture is excluded from packaged application sources.
const {safeStorage} = require('electron');
safeStorage.isAsyncEncryptionAvailable = async () => true;
safeStorage.encryptStringAsync = async value => Buffer.from(`fixture:${value}`);
safeStorage.decryptStringAsync = async value => ({result: value.toString().slice('fixture:'.length), shouldReEncrypt: false});
const {Backend} = require('../../src/backend.cjs');
const originalCall = Backend.prototype.call;
Backend.prototype.call = async function(method, params) {
  if (method === 'coordination.usage' && globalThis.holdWorkspaceUsage) await new Promise(resolve => {globalThis.releaseWorkspaceUsage = resolve;});
  return originalCall.call(this, method, params);
};
// Exercise the renderer protocol when a non-streamed revision replaces the
// initial token text. This is a UI fixture, not real self-evaluation evidence.
const originalStream = Backend.prototype.startStream;
Backend.prototype.startStream = function(id, sessionID, message, emit, onAdmission) {
  return originalStream.call(this, id, sessionID, message, event => {
    if (message === 'Write the workspace JSON.' && event.type === 'done') event = {...event, data: {...event.data, response: 'Saved workspace JSON. Final revised answer.'}};
    emit(event);
  }, onAdmission);
};
const originalStart = Backend.prototype.start;
Backend.prototype.start = async function(...args) {
  let stderr='';const spawn=this.spawnProcess;
  this.spawnProcess=(...input)=>{const child=spawn(...input);child.stderr.on('data',chunk=>{stderr=(stderr+chunk.toString()).slice(-12000);});return child;};
  let result;
  try {result=await originalStart.apply(this,args);} catch(error) {throw new Error(`${error.message}\n${this.publicValue(stderr)}`);} finally {this.spawnProcess=spawn;}
  globalThis.workspaceFixtureRPC = (method, params) => this.call(method, params);
  return result;
};
require('../../src/main.cjs');
