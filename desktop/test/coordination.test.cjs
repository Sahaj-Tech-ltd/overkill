const {test} = require('node:test');
const assert = require('node:assert/strict');
const {validate} = require('../src/coordination.cjs');
const sessionID = 'a1-b2';
test('coordination IPC permits only narrow session-bound operations', () => {
  assert.deepEqual(validate('register', {sessionID, name: 'frontend', role: 'Build UI'}), {session_id: sessionID, name: 'frontend', role: 'Build UI'});
  assert.deepEqual(validate('send', {sessionID, to: 'backend', message: 'Update?', key: 'request-1'}), {session_id: sessionID, to: 'backend', kind: 'request', payload: 'Update?', idempotency_key: 'request-1'});
  assert.deepEqual(validate('messages', {sessionID, beforeID: 'message-1'}), {session_id: sessionID, limit: 50, before_id: 'message-1'});
  assert.deepEqual(validate('cancel', {sessionID, messageID: 'message-1', revision: 2}), {session_id: sessionID, message_id: 'message-1', expected_revision: 2});
  assert.deepEqual(validate('peers', {sessionID}), {session_id: sessionID});
});
test('coordination IPC rejects impersonation, excessive data, unsafe cursors and malformed revisions', () => {
  for (const operation of ['register', 'peers', 'messages', 'send', 'cancel']) {
    assert.throws(() => validate(operation, {sessionID, from: 'forged'}));
    assert.throws(() => validate(operation, {sessionID, token: 'secret'}));
    assert.throws(() => validate(operation, {sessionID: '../outside'}));
    assert.throws(() => validate(operation, []));
  }
  assert.throws(() => validate('arbitrary.rpc', {sessionID}));
  for (const name of ['Backend', 'bad name', '', 'a'.repeat(65)]) assert.throws(() => validate('register', {sessionID, name, role: 'role'}));
  for (const role of ['', ' ', '界'.repeat(86)]) assert.throws(() => validate('register', {sessionID, name: 'frontend', role}));
  for (const message of ['', ' ', '界'.repeat(5334)]) assert.throws(() => validate('send', {sessionID, to: 'backend', message, key: 'request-1'}));
  assert.throws(() => validate('send', {sessionID, to: 'backend', message: 'hi', key: '../key'}));
  assert.throws(() => validate('messages', {sessionID, beforeID: ''}));
  for (const revision of [0, -1, 1.5, NaN, '1', Number.MAX_SAFE_INTEGER + 1]) assert.throws(() => validate('cancel', {sessionID, messageID: 'message-1', revision}));
});

test('task inspection IPC stays bounded and cannot mutate contracts or impersonate peers', () => {
  const {methods} = require('../src/coordination.cjs');
  assert.equal(methods.message, 'coordination.message');
  assert.equal(methods.verification, 'tests.results');
  assert.deepEqual(validate('message', {sessionID, messageID: 'message-1', afterRevision: 2}), {session_id: sessionID, message_id: 'message-1', after_revision: 2, limit: 25});
  assert.deepEqual(validate('usage', {sessionID, messageID: 'message-1'}), {session_id: sessionID, message_id: 'message-1'});
  assert.deepEqual(validate('contracts', {sessionID}), {session_id: sessionID, input: {action: 'list'}});
  assert.deepEqual(validate('readiness', {sessionID, contractID: 'contract-1'}), {session_id: sessionID, input: {action: 'readiness', contract_id: 'contract-1'}});
  for (const operation of ['message', 'usage', 'escalations', 'contracts', 'readiness', 'verification', 'evaluation']) {
    for (const extra of [{from: 'forged'}, {token: 'secret'}, {action: 'delete'}, {method: 'arbitrary.rpc'}]) assert.throws(() => validate(operation, {sessionID, ...extra}));
  }
  for (const cursor of [-1, '1', 1.1, Infinity, Number.MAX_SAFE_INTEGER + 1]) {
    assert.throws(() => validate('message', {sessionID, messageID: 'message-1', afterRevision: cursor}));
    assert.throws(() => validate('escalations', {sessionID, after: cursor}));
  }
});

test('root request token limits and owned task names are explicit optional controls', () => {
  const input = {sessionID, to: 'backend', message: 'Build endpoint', key: 'request-1', taskID: 'users', tokenLimit: 1000};
  const params = validate('send', input);
  assert.equal(params.task_id, 'users'); assert.deepEqual(params.usage_budget, {tokens: 1000, cost_usd: 0});
  for (const tokenLimit of [-1, NaN, Infinity, 1.5, '100', 1e12 + 1]) assert.throws(() => validate('send', {...input, tokenLimit}));
  assert.throws(() => validate('send', {...input, taskID: '界'.repeat(43)}));
  assert.throws(() => validate('send', {...input, usage_budget: {tokens: 999}}));
});

test('resending keeps unnamed conversation scope and both budget components', () => {
  const input = {sessionID, to: 'backend', message: 'Retry', key: 'retry-2', conversationID: 'original-conversation', tokenLimit: 100, costLimit: 2};
  const result = validate('send', input);
  assert.equal(result.conversation_id, 'original-conversation'); assert.deepEqual(result.usage_budget, {tokens:100,cost_usd:2});
  for (const costLimit of [-1,NaN,Infinity,'2',1000001]) assert.throws(()=>validate('send',{...input,costLimit}));
});

test('contract acknowledgement fixes action and derives the peer from the selected session', () => {
  const {methods}=require('../src/coordination.cjs');
  assert.equal(methods.acknowledge,'coordination.contract');
  const input={sessionID,contractID:'contract-1',revision:3};
  assert.deepEqual(validate('acknowledge',input),{session_id:sessionID,input:{action:'acknowledge',contract_id:'contract-1',expected_revision:3}});
  for(const extra of [{from:'backend'},{peer_id:'backend'},{action:'update'},{spec:{}},{token:'secret'},{method:'arbitrary.rpc'}])assert.throws(()=>validate('acknowledge',{...input,...extra}));
  for(const revision of [0,-1,1.2,NaN,Infinity,'1',Number.MAX_SAFE_INTEGER+1])assert.throws(()=>validate('acknowledge',{...input,revision}));
  assert.throws(()=>validate('acknowledge',{...input,contractID:'../outside'}));
});

test('contract edits constrain spec fields and keep owner and workspace server-derived',()=>{
  const spec={name:'Users',endpoint:'GET /users',schema_version:'1',schema:{type:'object'},acceptance_criteria:['Returns users'],owned_paths:['frontend'],artifacts:[],dependencies:[],compatibility_changes:[]};
  assert.deepEqual(validate('createContract',{sessionID,spec}),{session_id:sessionID,input:{action:'create',spec}});
  assert.deepEqual(validate('updateContract',{sessionID,spec,contractID:'contract-1',revision:2}),{session_id:sessionID,input:{action:'update',spec,contract_id:'contract-1',expected_revision:2}});
  for(const extra of [{owner_peer_id:'other'},{workspace:'/other'},{action:'delete'},{from:'other'},{project_id:'other'}])assert.throws(()=>validate('createContract',{sessionID,spec:{...spec,...extra}}));
  for(const extra of [{action:'delete'},{owner:'other'},{token:'secret'},{method:'arbitrary.rpc'}])assert.throws(()=>validate('createContract',{sessionID,spec,...extra}));
  for(const bad of [{schema:[]},{dependencies:[{contract_id:'dep',revision:0}]},{artifacts:[{name:'x',path:'x',owner:'other'}]},{acceptance_criteria:[]},{name:'x'.repeat(257)},{schema:{x:'x'.repeat(62000)}}])assert.throws(()=>validate('createContract',{sessionID,spec:{...spec,...bad}}));
  for(const revision of [0,'1',1.2,NaN])assert.throws(()=>validate('updateContract',{sessionID,spec,contractID:'contract-1',revision}));
});

test('prerequisites cross fixed IPC as bounded immutable request IDs',()=>{
 const first='11111111-1111-4111-8111-111111111111';
 const input={sessionID,to:'backend',message:'Use schema',key:'dependency',dependsOn:[first]};
 assert.deepEqual(validate('send',input).depends_on,[first]);
 assert.deepEqual(validate('graph',{sessionID,messageID:first}),{session_id:sessionID,message_id:first,limit:100});
 for(const dependsOn of [null,'bad',[first,first],['../outside'],Array(33).fill(first)]) assert.throws(()=>validate('send',{...input,dependsOn}));
 assert.throws(()=>validate('graph',{sessionID,messageID:first,from:'other'}));
});

test('required agreements cross IPC with exact pins and no caller-selected authority',()=>{
 const pin={contract_id:'11111111-1111-4111-8111-111111111111',revision:2};
 const input={sessionID,to:'backend',message:'Use reviewed schema',key:'pin-request',requiredContracts:[pin]};
 assert.deepEqual(validate('send',input).required_contracts,[pin]);
 for(const requiredContracts of [null,[{...pin,revision:0}],[{...pin,revision:'2'}],[{...pin,revision:9007199254740992}],[pin,pin],Array(33).fill(pin),[{...pin,peer_id:'other'}]])assert.throws(()=>validate('send',{...input,requiredContracts}));
});

test('repository registration is an explicit boolean and cannot select repository authority',()=>{
 const input={sessionID,name:'frontend',role:'UI'};
 for(const repositoryScope of [true,false])assert.deepEqual(validate('register',{...input,repositoryScope}),{session_id:sessionID,name:'frontend',role:'UI',repository_scope:repositoryScope});
 for(const repositoryScope of ['true',1,null,{},[]])assert.throws(()=>validate('register',{...input,repositoryScope}));
 for(const extra of [{project_id:'repository:forged'},{repository:{root:'/other'}},{workspace:'/other'}])assert.throws(()=>validate('register',{...input,...extra}));
});

test('request review IPC fixes replay action and excludes forged budget authority',()=>{
 const input={sessionID,messageID:'22222222-2222-4222-8222-222222222222',revision:3,usageRevision:2,reviewID:'11111111-1111-4111-8111-111111111111',note:'Reviewed prior writes.',tokenLimit:1000,costLimit:.5,requeue:true};
 assert.deepEqual(validate('review',input),{session_id:sessionID,input:{message_id:'22222222-2222-4222-8222-222222222222',expected_revision:3,expected_usage_revision:2,review_id:input.reviewID,note:input.note,limits:{tokens:1000,cost_usd:.5},requeue:true}});
 for(const extra of [{from:'other'},{owner_peer_id:'other'},{project_id:'other'},{action:'completed'},{clear_usage:true}])assert.throws(()=>validate('review',{...input,...extra}));
 for(const bad of [{revision:0},{usageRevision:0},{reviewID:'../bad'},{note:' '},{tokenLimit:-1},{costLimit:NaN},{requeue:false},{requeue:'true'}])assert.throws(()=>validate('review',{...input,...bad}));
});
