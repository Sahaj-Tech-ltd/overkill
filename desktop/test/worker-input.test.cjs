'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const {validateWorkers}=require('../src/worker-input.cjs');
const message={operation:'send',sessionID:'chat-one',to:'frontend',kind:'update',content:'Schema published.',idempotencyKey:'stable-key'};
test('worker IPC binds scope and rejects forged sender or lifecycle authority',()=>{
 for(const value of [{from:'backend'},{sender:'parent'},{scope:'foreign'},{incarnation:'old'},{method:'estop'},{human:true}])assert.throws(()=>validateWorkers({...message,...value}),/Unexpected/);
 for(const operation of ['cancel','start','followup','__proto__'])assert.throws(()=>validateWorkers({...message,operation}),/Unknown/);
 assert.throws(()=>validateWorkers({...message,sessionID:'../foreign'}));
 assert.throws(()=>validateWorkers({...message,kind:'request'}));
 const parsed=validateWorkers(message);assert.equal(parsed.method,'workers.send');assert.deepEqual(parsed.params,{session_id:'chat-one',input:{action:'send',to:'frontend',kind:'update',content:'Schema published.',idempotency_key:'stable-key'}});
});
test('worker IPC bounds message bytes and inbox polling',()=>{
 assert.throws(()=>validateWorkers({...message,content:'é'.repeat(8001)}));
 assert.throws(()=>validateWorkers({...message,idempotencyKey:''}));
 assert.throws(()=>validateWorkers({operation:'inbox',sessionID:'chat-one',after:-1}));
 assert.throws(()=>validateWorkers({operation:'inbox',sessionID:'chat-one',waitMS:1001}));
 assert.equal(validateWorkers({operation:'inbox',sessionID:'chat-one',after:42,waitMS:500}).params.input.after,42);
});
