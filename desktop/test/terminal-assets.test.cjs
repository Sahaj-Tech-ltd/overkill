'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const {terminalAsset,terminalResponse} = require('../src/terminal-assets.cjs');
test('terminal protocol serves only exact isolated emulator assets', async () => {
  assert.equal(terminalAsset('overkill-terminal://view/xterm.js'),'xterm.js');
  for (const url of ['file:///etc/passwd','overkill-terminal://view/main.cjs','overkill-terminal://view/../main.cjs','overkill-terminal://view/%78term.js','overkill-terminal://other/xterm.js','overkill-terminal://user@view/xterm.js','overkill-terminal://view/xterm.js?x=1','overkill-terminal://view/xterm.js#x']) assert.equal(terminalAsset(url),null,url);
  const response = await terminalResponse({url:'overkill-terminal://view/terminal-frame.html',method:'GET'});
  assert.equal(response.status,200);assert.match(response.headers.get('content-type'),/text\/html/);
  assert.equal(response.headers.get('x-content-type-options'),'nosniff');
  assert.match(await response.text(),/connect-src 'none'/);
  assert.equal((await terminalResponse({url:'overkill-terminal://view/xterm.js',method:'POST'})).status,404);
});
