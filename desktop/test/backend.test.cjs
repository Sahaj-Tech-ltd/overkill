const {test} = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');
const {once} = require('node:events');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const {Backend, validateReady, parseSSE} = require('../src/backend.cjs');
const token = 'a'.repeat(64);
test('ready protocol accepts only authenticated IPv4 loopback service', () => {
  assert.deepEqual(validateReady({type: 'ready', protocol: 1, address: 'http://127.0.0.1:12345', token}), {address: 'http://127.0.0.1:12345', token});
  for (const address of ['http://example.com:12345', 'http://127.0.0.1:12345/evil', 'http://user@127.0.0.1:12345', 'file:///etc/passwd', 'http://localhost:12345', 'http://127.0.0.1:12345?token=x']) assert.throws(() => validateReady({type: 'ready', protocol: 1, address, token}));
});
test('SSE preserves split UTF8 and fails truncated or malformed data', async () => {
  const bytes = Buffer.from('event: text\r\ndata: {"content":"😊"}\r\n\r\nevent: done\ndata: {}\n\n');
  async function* chunks() { for (const byte of bytes) yield Buffer.from([byte]); }
  const result = []; for await (const event of parseSSE(chunks())) result.push(event);
  assert.deepEqual(result, [{type: 'text', data: {content: '😊'}}, {type: 'done', data: {}}]);
  async function* truncated() { yield Buffer.from('event: text\ndata: {}\n'); }
  await assert.rejects(async () => { for await (const _ of parseSSE(truncated())) {} }, /during an event/);
});
test('stream reports disconnect as failure and authenticates every request', async t => {
  const server = http.createServer((request, response) => {
    assert.equal(request.headers.authorization, `Bearer ${token}`);
    response.writeHead(200, {'content-type': 'text/event-stream'}); response.end('event: text\ndata: {"content":"partial"}\n\n');
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening'); t.after(() => server.close());
  const backend = new Backend({}); backend.credentials = {address: `http://127.0.0.1:${server.address().port}`, token};
  const events = []; await backend.stream('test', 'session', 'hello', value => events.push(value));
  assert.deepEqual(events.map(event => event.type), ['text', 'error']); assert.match(events[1].data.message, /before saving/); assert.equal(backend.streams.size, 0);
});
test('missing bundled executable fails promptly and repeated stop is safe', async t => {
  const home = await fs.mkdtemp(path.join(os.tmpdir(), 'overkill-missing-backend-')); t.after(() => fs.rm(home, {recursive: true, force: true}));
  const backend = new Backend({home, executable: path.join(home, 'missing'), postgresBin: home});
  await assert.rejects(backend.start({provider: 'openai', model: 'test', baseURL: '', apiKey: 'test-only'}), /could not be started/);
  await Promise.all([backend.stop(), backend.stop()]);
  assert.equal(backend.child, null); assert.equal(backend.secret, '');
});
test('provider error echoes never reveal credentials through the renderer bridge', () => {
  const backend = new Backend({}); backend.secret = 'secret-with-"quote'; backend.credentials = {token};
  assert.deepEqual(backend.publicValue({message: `bad ${backend.secret}`, nested: [token]}), {message: 'bad [redacted]', nested: ['[redacted]']});
});

test('stop during configuration publication prevents a late backend spawn', async () => {
  let release, entered; let spawns = 0;
  const writing = new Promise(resolve => { entered = resolve; });
  const backend = new Backend({home: '/fixture', writeConfig: () => { entered(); return new Promise(resolve => { release = resolve; }); }, spawnProcess: () => { spawns++; throw new Error('must not spawn'); }});
  const start = assert.rejects(backend.start({provider: 'openai', model: 'fixture', baseURL: '', apiKey: 'fixture-key'}), /startup was cancelled/);
  await writing; await backend.stop(); release(); await start;
  assert.equal(spawns, 0); assert.equal(backend.child, null); assert.equal(backend.secret, '');
});

test('stream admission reserves synchronously and rejects actual HTTP409 without emitting accepted history',async t=>{
 let release;const server=http.createServer((req,res)=>{assert.equal(req.headers.authorization,`Bearer ${token}`);release=()=>{res.writeHead(409,{'Content-Type':'application/json'});res.end('{"error":"busy"}');};});server.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>server.close());const backend=new Backend({});backend.credentials={address:`http://127.0.0.1:${server.address().port}`,token};const events=[];const started=backend.startStream('request','session','Draft',event=>events.push(event),()=>events.push('admitted'));assert.equal(backend.streams.size,1);assert.throws(()=>backend.startStream('duplicate','session','Draft',()=>{}),/already running/);while(!release)await new Promise(resolve=>setImmediate(resolve));release();await assert.rejects(started.admission,/409/);await started.completion;assert.deepEqual(events,[]);assert.equal(backend.streams.size,0);
});

test('admission precedes immediate text/done and accepted disconnection retains partial output',async t=>{
 let partial=false;const server=http.createServer((req,res)=>{res.writeHead(200,{'Content-Type':'text/event-stream'});res.end('event: text\ndata: {"content":"partial"}\n\n'+(partial?'':'event: done\ndata: {"response":"final"}\n\n'));});server.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>server.close());const backend=new Backend({});backend.credentials={address:`http://127.0.0.1:${server.address().port}`,token};for(const broken of [false,true]){partial=broken;const events=[];const started=backend.startStream('request','session','Draft',event=>events.push(event),()=>events.push({type:'admitted'}));assert.deepEqual(await started.admission,{id:'request',accepted:true});await started.completion;assert.deepEqual(events.map(event=>event.type),['admitted','text',broken?'error':'done']);if(broken){assert.equal(events[1].data.content,'partial');assert.match(events[2].data.message,/before saving/);}assert.equal(backend.streams.size,0);}
});
