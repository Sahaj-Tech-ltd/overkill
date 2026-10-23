import assert from 'node:assert/strict';
import test from 'node:test';
import React from 'react';
import { render } from 'ink';
import { PassThrough, Writable } from 'node:stream';
import { ConfigTab } from '../dist/components/settings/ConfigTab.js';

const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function waitFor(predicate) {
  const end = Date.now() + 3000;
  while (!predicate()) {
    if (Date.now() > end) throw new Error('Settings UI did not reach expected state');
    await pause(20);
  }
}

test('sandbox is displayed unavailable and cannot issue an enable RPC', async () => {
  const calls = [];
  const backend = { async call(method, params) {
    calls.push({ method, params });
    if (method === 'config.get') return {
      version: 1, agent: {}, ui: {}, thinking: { level: 'off' }, system_prompt: '',
      // A stale/older server value must not make this UI claim isolation.
      security: { autonomy_level: 'supervised', sandbox_enabled: true },
    };
    throw new Error(`Unexpected settings mutation: ${method}`);
  }};
  const stdin = new PassThrough();
  stdin.isTTY = true; stdin.setRawMode = () => {}; stdin.ref = () => {}; stdin.unref = () => {};
  let output = '';
  const stdout = new Writable({ write(chunk, _encoding, done) { output += chunk.toString(); done(); } });
  stdout.columns = 160; stdout.rows = 70; stdout.isTTY = false;
  const app = render(React.createElement(ConfigTab, { backend, contentHeight: 60 }),
    { stdin, stdout, stderr: stdout, debug: true, patchConsole: false, exitOnCtrlC: false });
  try {
    await waitFor(() => output.includes('Unavailable') && output.includes('tools run on the host'));
    assert.doesNotMatch(output, /Sandbox enabled/);
    stdin.write('sandbox'); await pause(40);
    stdin.write('\u001b[B'); await pause(40);
    stdin.write(' '); stdin.write('\r'); stdin.write('\u001b[C'); await pause(80);
    assert.deepEqual(calls.map((call) => call.method), ['config.get']);
  } finally { app.unmount(); stdin.end(); }
});
