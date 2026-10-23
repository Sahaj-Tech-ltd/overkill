import assert from 'node:assert/strict';
import test from 'node:test';
import React from 'react';
import { render, Text } from 'ink';
import { PassThrough, Writable } from 'node:stream';
import { useChat } from '../dist/hooks/use-chat.js';

async function waitFor(predicate) {
  const end = Date.now() + 3000;
  while (!predicate()) {
    if (Date.now() > end) throw new Error('Chat hook did not reach expected state');
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
}

for (const fixture of [
  { name: 'revised final answer replaces already displayed draft', draft: 'Initial draft', response: 'Evaluated final answer', expected: 'Evaluated final answer' },
  { name: 'done without a final answer preserves streamed text', draft: 'Streamed answer', expected: 'Streamed answer' },
  { name: 'done with no text deltas displays the final answer', draft: '', response: 'Auto mode enabled', expected: 'Auto mode enabled' },
]) {
  test(`actual useChat: ${fixture.name}`, async () => {
    let release;
    const gate = new Promise((resolve) => { release = resolve; });
    const calls = [];
    const backend = {
      async *streamCall(method, params) {
        calls.push({ method, params });
        if (fixture.draft) yield { type: 'text', content: fixture.draft };
        await gate;
        // Mirrors the flattened done.response field emitted by the Go SSE adapter.
        yield { type: 'done', response: fixture.response, model: 'fixture/model' };
      },
    };
    let chat;
    function Probe() {
      chat = useChat(backend);
      return React.createElement(Text, null, chat.messages.at(-1)?.content ?? '');
    }
    const stdin = new PassThrough();
    stdin.isTTY = true; stdin.setRawMode = () => {}; stdin.ref = () => {}; stdin.unref = () => {};
    const stdout = new Writable({ write(_chunk, _encoding, done) { done(); } });
    stdout.columns = 100; stdout.rows = 30; stdout.isTTY = false;
    const app = render(React.createElement(Probe), { stdin, stdout, stderr: stdout, debug: true, patchConsole: false, exitOnCtrlC: false });
    try {
      await waitFor(() => chat !== undefined);
      const sending = chat.sendMessage('work');
      await waitFor(() => chat.isLoading && chat.messages.at(-1)?.content === fixture.draft);
      assert.equal(calls.length, 1);
      assert.equal(calls[0].method, 'agent.send');
      release();
      await sending;
      await waitFor(() => !chat.isLoading && chat.messages.at(-1)?.content === fixture.expected);
      assert.equal(chat.messages.at(-1)?.role, 'assistant');
      assert.equal(chat.error, null);
    } finally {
      release(); app.unmount(); stdin.end();
    }
  });
}
