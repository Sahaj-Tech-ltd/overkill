'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const {validateTerminal} = require('../src/terminal-input.cjs');
const id = '31befbc2-c8e6-4d10-92c0-9226899457cd';
const base = {operation:'input',sessionID:'chat-1',id,data:'hello\r',controlRevision:1,sequence:1};

test('terminal IPC rejects authority changes and unsupported launch operations', () => {
  for (const extra of [{human:true},{owner:'other'},{session_id:'other'},{command:'sh'},{method:'agent.abort'}]) {
    assert.throws(() => validateTerminal({...base,...extra}), /Unexpected/);
  }
  for (const operation of ['start','__proto__','constructor']) assert.throws(() => validateTerminal({...base,operation}), /Unknown/);
  assert.throws(() => validateTerminal({...base,sessionID:'../other'}), /Choose a chat/);
  assert.throws(() => validateTerminal({...base,id:'../../other'}), /valid terminal/);
});

test('terminal IPC preserves raw keys and binary input with bounded canonical encoding', () => {
  assert.deepEqual(validateTerminal(base), {method:'terminal.input',params:{session_id:'chat-1',id,control_revision:1,sequence:1,data:'hello\r'}});
  const bytes = Buffer.from([0,3,27,91,65,255]).toString('base64');
  assert.equal(validateTerminal({operation:'input',sessionID:'chat-1',id,bytes,controlRevision:1,sequence:1}).params.bytes,bytes);
  for (const input of [{...base,bytes},{...base,data:'é'.repeat(8193)},{operation:'input',sessionID:'chat-1',id,bytes:'%%%'}]) assert.throws(() => validateTerminal(input));
});

test('terminal IPC fences output bounds, dimensions and takeover flags', () => {
  for (const extra of [{cursor:-1},{cursor:Number.MAX_SAFE_INTEGER+1},{waitMS:1001},{limit:65537}]) assert.throws(() => validateTerminal({operation:'read',sessionID:'chat-1',id,...extra}));
  for (const rows of [0,1001,1.2,NaN]) assert.throws(() => validateTerminal({operation:'resize',sessionID:'chat-1',id,controlRevision:1,rows,cols:80}));
  assert.throws(() => validateTerminal({operation:'control',sessionID:'chat-1',id,controlRevision:1,release:'true'}));
  assert.equal(validateTerminal({operation:'control',sessionID:'chat-1',id,release:true,controlRevision:1}).params.release,true);
});

test('every terminal effect requires a safe control revision and input requires a sequence',()=>{
  for(const operation of ['input','interrupt','resize','control','close','handoff']){
    const input={...base,operation,rows:24,cols:80};
    for(const controlRevision of [undefined,0,-1,1.5,Number.MAX_SAFE_INTEGER+1,'1'])assert.throws(()=>validateTerminal({...input,controlRevision}));
  }
  for(const operation of ['input','interrupt'])for(const sequence of [undefined,0,-1,1.5,Number.MAX_SAFE_INTEGER+1,'1'])assert.throws(()=>validateTerminal({...base,operation,sequence}));
  assert.equal(validateTerminal({operation:'handoff',sessionID:'chat-1',id,controlRevision:1}).method,'terminal.handoff');
  assert.throws(()=>validateTerminal({operation:'handoff',sessionID:'chat-1',id,controlRevision:1,to:'other-worker'}),/Unexpected/);
});
