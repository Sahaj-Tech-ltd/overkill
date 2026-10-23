const {test} = require('node:test');
const assert = require('node:assert/strict');
const {create} = require('../src/flows.js');
class Element {
  constructor() { this.children = []; this.hidden = false; this.listeners = {}; this.textContent = ''; }
  append(...items) { this.children.push(...items); }
  replaceChildren(...items) { this.children = items; this.textContent = ''; }
  addEventListener(name, fn) { this.listeners[name] = fn; }
}
test('late flow status cannot replace another selected chat or refresh a local response', async () => {
  const region = new Element(), document = {getElementById: () => region, createElement: () => new Element()};
  let session = {id: 'first'}, resolve, idle = true, reads = 0; const refreshed = [];
  const api = {flows: async () => { reads++; return await new Promise(done => { resolve = done; }); }};
  const view = create({api, document, getSession: () => session, canRefresh: () => idle, refreshHistory: async (id, current) => { if (current()) refreshed.push(id); }});
  const pending = view.sync(); session = {id: 'second'}; view.reset();
  resolve({flows: [{id: 'old', status: 'completed'}]}); await pending;
  assert.deepEqual(refreshed, []); assert.equal(region.hidden, true);
  idle = false; await view.sync(); assert.equal(reads, 1);
  idle = true; const next = view.sync(); resolve({flows: [{id: 'new', status: 'completed'}]}); await next;
  assert.deepEqual(refreshed, ['second']); assert.equal(region.children[0].textContent, 'Saved work completed');
});
test('pause targets the bound chat and history refresh never resets an editable draft', async () => {
  const region = new Element(), document = {getElementById: () => region, createElement: () => new Element()};
  let status = 'queued', draft = 'unsent text', calls = 0; const stopped = [];
  const api = {flows: async () => ({flows: [{id: 'flow', status, generation: 1}]}), stop: async id => { stopped.push(id); status = 'paused'; }};
  const view = create({api, document, getSession: () => ({id: 'selected'}), canRefresh: () => true, refreshHistory: async (_id, current) => { assert.equal(current(), true); calls++; }});
  await view.sync(); const pause = region.children[1]; assert.equal(pause.textContent, 'Pause continuation');
  await pause.listeners.click(); assert.deepEqual(stopped, ['selected']); assert.equal(region.children[0].textContent, 'Continuation paused');
  assert.equal(draft, 'unsent text'); assert.equal(calls, 2);
});
