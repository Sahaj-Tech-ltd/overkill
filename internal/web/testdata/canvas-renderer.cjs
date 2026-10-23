const assert = require('node:assert/strict');
const path = require('node:path');
const {chromium} = require(path.resolve(__dirname, '../../../desktop/node_modules/playwright'));
const state = (sessionId, revision, text) => ({sessionId, revision, surfaces: [{surfaceId: 'main', catalog: 'overkill.canvas.v1', root: 'root', rendering: true, data: {name: text, count: 3, nil: null, nested: {}, 'a/b': {'~key': true}}, components: [{id: 'root', type: 'Column', children: ['title', 'bound', 'number', 'missing', 'object', 'escaped', 'divider', 'nil', 'row']}, {id: 'nil', type: 'Text', textPath: '/nil'}, {id: 'row', type: 'Row', children: []}, {id: 'title', type: 'Heading', text: 'Canvas heading'}, {id: 'bound', type: 'Text', textPath: '/name'}, {id: 'number', type: 'Text', textPath: '/count'}, {id: 'missing', type: 'Text', textPath: '/absent'}, {id: 'object', type: 'Text', textPath: '/nested'}, {id: 'escaped', type: 'Text', textPath: '/a~1b/~0key'}, {id: 'divider', type: 'Divider'}]}]});
(async () => {
 const browser = await chromium.launch({headless: true});
 try {
  const context = await browser.newContext(); const page = await context.newPage();
  await page.addInitScript(() => { const Native = window.WebSocket; window.canvasFixtureSockets = []; window.WebSocket = class extends Native { constructor(...args) { super(...args); window.canvasFixtureSockets.push(this); } }; });
  const base = process.env.OVERKILL_WEB_TEST_URL, token = process.env.OVERKILL_WEB_TEST_TOKEN;
  const login = await context.request.post(base + '/api/auth', {headers: {Authorization: 'Bearer ' + token}}); assert.equal(login.status(), 204);
  const states = {a: state('a', 1, '<img src=x onerror="window.canvasInjected=true">'), b: state('b', 9, 'Session B')};
  let holdA = false, heldA, stateCalls = 0;
  await page.route('**/api/sessions', route => route.fulfill({json: [{id: 'a', title: 'Canvas A'}, {id: 'b', title: 'Canvas B'}]}));
  await page.route('**/api/sessions/*', route => route.fulfill({json: {messages: []}}));
  await page.route('**/api/canvas/state?*', async route => {
   stateCalls++; const sid = new URL(route.request().url()).searchParams.get('sessionId');
   if (sid === 'a' && holdA) {heldA = route; return;}
   await route.fulfill({json: states[sid]});
  });
  await page.goto(base); await page.locator('#conn-dot[data-state="ok"]').waitFor();
  await page.locator('#canvas-toggle').click();
  await page.locator('#canvas-content[data-canvas-revision="1"]').waitFor();
  assert.equal(await page.locator('[data-component-id="bound"]').textContent(), states.a.surfaces[0].data.name);
  assert.equal(await page.locator('[data-component-id="number"]').textContent(), '3');
  assert.equal(await page.locator('[data-component-id="nil"]').textContent(), 'null');
  assert.equal(await page.locator('.canvas-row').count(), 1);
  assert.equal(await page.locator('[data-component-id="missing"]').textContent(), '[Missing value]');
  assert.equal(await page.locator('[data-component-id="object"]').textContent(), '[Value cannot be displayed]');
  assert.equal(await page.locator('[data-component-id="escaped"]').textContent(), 'true');
  assert.equal(await page.locator('#canvas-content img').count(), 0); assert.equal(await page.evaluate(() => window.canvasInjected), undefined);
  const href = new URL(await page.locator('#canvas-snapshot').getAttribute('href'), base);
  assert.equal(href.pathname, '/api/canvas/snapshot'); assert.equal(href.searchParams.get('sessionId'), 'a'); assert.equal(href.searchParams.get('surfaceId'), 'main');
  // Shared renderer fails atomically for invalid schema/graphs and UTF8 bounds.
  const staged = await page.evaluate(() => {
   const output = document.createElement('div');
   window.OverkillCanvas.render(output, {sessionId: 'a', revision: 0, surfaces: [{surfaceId: 'staged', catalog: 'overkill.canvas.v1', root: '', components: [], data: {}, rendering: false}]}, 'staged');
   return {text: output.textContent, surface: output.dataset.surfaceId, revision: output.dataset.canvasRevision};
  }); assert.equal(staged.revision, '0'); assert.equal(staged.surface, 'staged'); assert.match(staged.text, /not started rendering/);
  const invalid = await page.evaluate(original => {
   const good = structuredClone(original), output = document.createElement('div'); window.OverkillCanvas.render(output, good, 'main');
   const before = output.innerHTML;
   const mutations = [s => s.surfaces[0].components.find(c => c.id === 'title').html = '<script>bad()</script>', s => s.surfaces[0].components.find(c => c.id === 'title').type = 'Script', s => s.surfaces[0].components[0].children = ['root'], s => s.surfaces[0].components[0].children = ['missing-id'], s => s.surfaces[0].components.find(c => c.id === 'title').text = '😀'.repeat(4097), s => s.surfaces[0].data.name = '😀'.repeat(4097), s => s.surfaces[0].components.find(c => c.id === 'bound').textPath = '', s => s.surfaces[0].components.find(c => c.id === 'bound').text = 'ambiguous', s => s.surfaces[0].components.push({id: 'unreachable', type: 'Column', children: ['unreachable']}), s => s.surfaces[0].data.oversized = 'x'.repeat(65537), s => {s.surfaces = Array.from({length: 17}, (_, i) => ({...s.surfaces[0], surfaceId: String(i)}));}];
   return mutations.map(change => {const next = structuredClone(good); change(next); try {window.OverkillCanvas.render(output, next, 'main'); return false;} catch {return output.innerHTML === before;}});
  }, states.a); assert.ok(invalid.every(Boolean));
  const publish = sid => context.request.post(base + '/fixture/canvas-event', {headers: {Authorization: 'Bearer ' + token}, data: {sessionId: sid}});
  holdA = true; await publish('a');
  for (let i = 0; !heldA && i < 100; i++) await new Promise(resolve => setTimeout(resolve, 10)); assert.ok(heldA);
  await page.locator('.session-row').filter({hasText: 'Canvas B'}).click();
  await page.locator('#canvas-content[data-canvas-revision="9"]').waitFor();
  holdA = false; await heldA.fulfill({json: state('a', 100, 'STALE A')}).catch(() => {});
  assert.equal(await page.locator('#canvas-content').textContent().then(text => text.includes('STALE A')), false);
  assert.equal(await page.locator('#canvas-content[data-canvas-revision="9"]').count(), 1);
  states.b = state('b', 10, 'After reconnect');
  // Drop the real browser transport; WSStream must reconnect and refetch state.
  await page.evaluate(() => window.canvasFixtureSockets.at(-1).close());
  await page.locator('#canvas-content[data-canvas-revision="10"]').waitFor({timeout: 15000});
  assert.equal(await page.locator('[data-component-id="bound"]').textContent(), 'After reconnect');
  assert.ok(stateCalls >= 4);
  console.log('Canvas shared renderer DOM, bindings, injection rejection, stale sessions and reconnect passed');
 } finally {await browser.close();}
})().catch(error => {console.error(error); process.exitCode = 1;});
