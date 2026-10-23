import assert from 'node:assert/strict';
import test from 'node:test';
import {createServer} from 'node:http';
import React from 'react';
import {render} from 'ink';
import {PassThrough, Writable} from 'node:stream';
import {BackendClient} from '../dist/backend/client.js';
import {CoordinationClient, stateLabel, parseDependencies, parseContractPins} from '../dist/backend/coordination.js';
import {CoordinationModel} from '../dist/components/sidebar/coordination-model.js';
import {CoordinationPanel} from '../dist/components/sidebar/coordination-panel.js';
const self = {id:'self',name:'frontend',role:'UI',enabled:true};
const recipient = {id:'peer',name:'backend',role:'API',enabled:true};
const item = (overrides={}) => ({id:'message-1',from:'self',to:'peer',kind:'request',payload:'Implement endpoint',task_id:'original-task',conversation_id:'original-conversation',state:'blocked',revision:7,updated_at:'2026-10-22T00:00:00Z',...overrides});
const usage = {scope:'task',task_id:'original-task',limits:{tokens:500,cost_usd:1.25},input_tokens:13,output_tokens:5,cached_input_tokens:0,estimated_cost_usd:0.02,pending_executions:0,unconfirmed_executions:0,unknown_usage:1,unknown_price:1,requires_review:true,reason:'unknown usage'};
const page = (messages=[item()],more=false) => ({messages,has_more:more,before_id:messages.at(-1)?.id ?? ''});
const prerequisite = '11111111-1111-4111-8111-111111111111';
const graph = (message=item()) => ({root_id:message.id,nodes:[{message,readiness:'waiting',blockers:[{id:prerequisite,state:'accepted',reason:'prerequisite_incomplete'}]}],truncated:false,hidden_dependencies:false});
const detail = (message=item()) => ({message,events:[{revision:1,type:'accepted',payload:{},created_at:''},{revision:7,type:message.state,payload:{reason:'budget requires review'},created_at:''}],next_revision:7,has_more:false});
async function waitFor(predicate) {const end=Date.now()+3000;while(!predicate()){if(Date.now()>end)throw new Error('UI did not reach expected state');await new Promise((resolve)=>setTimeout(resolve,15));}}
function mount(element,onFrame=()=>{}) {
 const stdin=new PassThrough();stdin.isTTY=true;stdin.setRawMode=()=>{};stdin.ref=()=>{};stdin.unref=()=>{};
 let output='',lastFrame='';const stdout=new Writable({write(chunk,_encoding,done){lastFrame=chunk.toString();output+=lastFrame;done();onFrame(lastFrame);}});stdout.columns=110;stdout.rows=100;stdout.isTTY=false;
 const app=render(element,{stdin,stdout,stderr:stdout,debug:true,patchConsole:false,exitOnCtrlC:false});
 return {app,input:(value)=>stdin.write(value),output:()=>output,lastFrame:()=>lastFrame,close:()=>{app.unmount();stdin.end();}};
}
function fixture(handler=()=>undefined) {
 const calls=[];const backend={async call(method,params,options){calls.push({method,params,options});const result=handler(method,params,options);if(result!==undefined)return result;
 if(method==='coordination.graph')return graph();if(method==='coordination.peers')return {self,peers:[self,recipient]};if(method==='coordination.messages')return page();if(method==='coordination.usage')return {usage};if(method==='coordination.message')return detail();if(method==='coordination.cancel')return {message:item({state:'cancelled',revision:8})};if(method==='coordination.send')return {message:item({id:'new-message',state:'accepted',revision:1})};throw new Error(`Unexpected ${method}`);
 }};return {backend,calls};
}

test('typed client uses real HTTP contracts with scoped identity and optimistic cancel', async()=>{
 const calls=[];const server=createServer(async(req,res)=>{let body='';for await(const chunk of req)body+=chunk;const rpc=JSON.parse(body);calls.push({...rpc,authorization:req.headers.authorization});res.setHeader('content-type','application/json');res.end(JSON.stringify({jsonrpc:'2.0',id:rpc.id,result:rpc.method==='coordination.graph'?graph():rpc.method==='coordination.peers'?{self,peers:[self,recipient]}:rpc.method==='coordination.messages'?page():rpc.method==='coordination.message'?detail():rpc.method==='coordination.usage'?{usage}:{message:item()}}));});
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 try {
 const client=new CoordinationClient(new BackendClient(`http://127.0.0.1:${server.address().port}`,'fixture-token'));
 await client.peers('selected');await client.messages('selected','before');await client.detail('selected','message-1',3);await client.usage('selected','message-1');await client.cancel('selected',item());
 await client.send('selected',{to:'backend',message:'Real request',taskID:'original-task',conversationID:'original-conversation',tokens:'500',cost:'1.25',key:'unique-key',sender:'forged',project_id:'forged',dependencies:prerequisite,requiredContracts:`${prerequisite}@2`});
 await client.graph('selected','message-1');
 assert.ok(calls.every(c=>c.params.session_id==='selected'&&c.authorization==='Bearer fixture-token'));
 assert.deepEqual(calls[1].params,{session_id:'selected',limit:50,before_id:'before'});
 assert.deepEqual(calls[2].params,{session_id:'selected',message_id:'message-1',after_revision:3,limit:25});
 assert.deepEqual(calls[4].params,{session_id:'selected',message_id:'message-1',expected_revision:7});
 assert.deepEqual(calls[5].params,{session_id:'selected',to:'backend',kind:'request',payload:'Real request',idempotency_key:'unique-key',task_id:'original-task',conversation_id:'original-conversation',usage_budget:{tokens:500,cost_usd:1.25},depends_on:[prerequisite],required_contracts:[{contract_id:prerequisite,revision:2}]});
 assert.deepEqual(calls[6].params,{session_id:'selected',message_id:'message-1',limit:100});
 } finally {server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}
});

test('retry is draft only, locks original scopes and reuses idempotency key after ambiguous failure',async()=>{
 let sends=0;const {backend,calls}=fixture((method)=>{if(method==='coordination.send'&&++sends===1)throw new Error('Connection lost after submission');});
 const model=new CoordinationModel(backend);await model.setSession('selected');await model.retry(item({depends_on:[prerequisite],required_contracts:[{contract_id:prerequisite,revision:2}]}));
 const draft=model.getSnapshot().draft;assert.equal(calls.filter(c=>c.method==='coordination.send').length,0);
 assert.equal(draft.conversationID,'original-conversation');assert.equal(draft.taskID,'original-task');assert.equal(draft.tokens,'500');assert.equal(draft.cost,'1.25');
 assert.equal(draft.dependencies,prerequisite);assert.equal(draft.requiredContracts,`${prerequisite}@2`);
 for(const field of ['to','taskID','tokens','cost','dependencies','requiredContracts'])model.editDraft(field,'forged');
 assert.deepEqual(model.getSnapshot().draft,draft);
 await model.send();assert.match(model.getSnapshot().error,/Connection lost/);assert.equal(model.getSnapshot().draft.key,draft.key);
 await model.send();const sent=calls.filter(c=>c.method==='coordination.send');assert.equal(sent.length,2);assert.deepEqual(sent[0].params.depends_on,[prerequisite]);assert.deepEqual(sent[0].params.required_contracts,[{contract_id:prerequisite,revision:2}]);assert.deepEqual(sent[0].params,sent[1].params);assert.equal(model.getSnapshot().draft,null);
 model.dispose();
});

test('session replacement aborts requests, ignores stale data and clears drafts and details',async()=>{
 let resolveOld;let oldSignal;const {backend,calls}=fixture((method,params,options)=>{if(method==='coordination.peers'&&params.session_id==='old'){oldSignal=options.signal;return new Promise(resolve=>{resolveOld=resolve;});}});
 const model=new CoordinationModel(backend);const old=model.setSession('old');await model.setSession('new');assert.equal(oldSignal.aborted,true);
 await model.inspect(item());await model.retry(item());assert.ok(model.getSnapshot().draft);resolveOld({self:{...self,name:'STALE'},peers:[]});await old;
 assert.equal(model.getSnapshot().self.name,'frontend');assert.equal(model.getSnapshot().session,'new');
 await model.setSession('');assert.equal(model.getSnapshot().draft,null);assert.equal(model.getSnapshot().detail,null);assert.deepEqual(model.getSnapshot().page.messages,[]);
 assert.ok(calls.filter(c=>c.params.session_id==='old').length===1);model.dispose();
});

test('conflicting cancel keeps authoritative state; pagination and unknown lifecycle values stay explicit',async()=>{
 const {backend,calls}=fixture((method,params)=>{if(method==='coordination.cancel')throw new Error('Revision conflict; refresh');if(method==='coordination.messages')return params.before_id?page([item({id:'older'})]):page([item()],true);});
 const model=new CoordinationModel(backend);await model.setSession('selected');await model.cancel(item());assert.equal(model.getSnapshot().page.messages[0].state,'blocked');assert.match(model.getSnapshot().error,/Revision conflict/);
 await model.reload(true);assert.deepEqual(model.getSnapshot().page.messages.map(m=>m.id),['message-1','older']);
 assert.equal(calls.find(c=>c.params.before_id)?.params.before_id,'message-1');assert.equal(stateLabel('accepted'),'queued');assert.equal(stateLabel('started'),'working');assert.equal(stateLabel('future-state'),'future-state');
 const before=calls.length;await model.retry(item({from:'peer'}));assert.equal(calls.length,before);assert.equal(model.getSnapshot().draft,null);model.dispose();
});

test('Ink Agents panel displays persisted states and usage; retry needs explicit review send',async()=>{
 const {backend,calls}=fixture();const ui=mount(React.createElement(CoordinationPanel,{backend,sessionId:'selected',active:true}));
 try {
 await waitFor(()=>ui.output().includes('blocked'));assert.match(ui.output(),/Presence unknown/);assert.doesNotMatch(ui.output(),/online/);
 ui.input('\r');await waitFor(()=>ui.output().includes('original-conversation'));ui.input('v');await waitFor(()=>ui.output().includes('Unknown usage/price: 1/1'));ui.input('v');await waitFor(()=>ui.output().includes('Activity · v:next view'));assert.match(ui.output(),/budget requires review/);
 ui.input('y');await waitFor(()=>ui.output().includes('Retry draft'));assert.equal(calls.filter(c=>c.method==='coordination.send').length,0);
 assert.match(ui.output(),/original scope locked/);ui.input('\r');await waitFor(()=>calls.some(c=>c.method==='coordination.send'));
 const send=calls.find(c=>c.method==='coordination.send');assert.equal(send.params.conversation_id,'original-conversation');assert.deepEqual(send.params.usage_budget,{tokens:500,cost_usd:1.25});
 } finally {ui.close();}
});

test('Ink registration and new request collect reviewed text, inactive panel cannot send',async()=>{
 let registered=false;const {backend,calls}=fixture((method)=>{if(method==='coordination.graph')return graph();if(method==='coordination.peers')return {self:registered?self:null,peers:[recipient]};if(method==='coordination.register'){registered=true;return {peer:self};}});
 const element=(active)=>React.createElement(CoordinationPanel,{backend,sessionId:'selected',active});const previousActEnvironment=globalThis.IS_REACT_ACT_ENVIRONMENT;globalThis.IS_REACT_ACT_ENVIRONMENT=true;let ui;await React.act(async()=>{ui=mount(element(true));});
 const currentFrame=async(expected)=>{
  try{await waitFor(()=>ui.lastFrame().includes(expected)&&!ui.lastFrame().includes('Loading / saving…'));}
  catch(error){throw new Error(`${error.message}; expected settled ${JSON.stringify(expected)}; last frame:\n${ui.lastFrame()}`);}
 };
 const input=async(value,expected)=>{await React.act(async()=>{ui.input(value);});await currentFrame(expected);};
 try {
 await currentFrame('unregistered');await input('g','Agent name:');await input('frontend','Name: frontend');await input('\r','Role:');await input('UI engineer','UI engineer');await input('\r','Review registration');assert.equal(calls.filter(c=>c.method==='coordination.register').length,0);
 await input('\r','As frontend');assert.deepEqual(calls.find(c=>c.method==='coordination.register').params,{session_id:'selected',name:'frontend',role:'UI engineer'});
 await currentFrame('Correspondence (1)');await input('n','New request draft');await React.act(async()=>{ui.app.rerender(element(false));});await React.act(async()=>{ui.input('\r');ui.input('x');});assert.equal(calls.filter(c=>c.method==='coordination.send').length,0);
 } finally {await React.act(async()=>{ui.close();});globalThis.IS_REACT_ACT_ENVIRONMENT=previousActEnvironment;}
});

test('Ink presence ages to unknown without another successful transport response',async()=>{
 const presence={state:'working',source:'runtime_lease',observers:1,expires_at:new Date(Date.now()+500).toISOString()};
 const {backend,calls}=fixture(method=>method==='coordination.peers'?{self,peers:[{...self,presence},{...recipient,presence}]}:undefined);
 const ui=mount(React.createElement(CoordinationPanel,{backend,sessionId:'selected',active:true}));
 try {
  await waitFor(()=>ui.output().includes('backend: Working'));const mark=ui.output().length;
  await waitFor(()=>ui.output().slice(mark).includes('backend: Presence unknown'));
  assert.equal(calls.filter(call=>call.method==='coordination.peers').length,1,'local expiry must work before the next four-second refresh');
 } finally {ui.close();}
});


test('prerequisite UUIDs reject malformed, duplicate and excessive input before transport',async()=>{
 const {backend,calls}=fixture();const client=new CoordinationClient(backend);
 const draft={to:'backend',message:'Wait for endpoint',taskID:'',tokens:'',cost:'',key:'key'};
 for(const dependencies of ['bad-id',`${prerequisite}, ${prerequisite.toUpperCase()}`,Array.from({length:33},(_,i)=>`${String(i).padStart(8,'0')}-1111-4111-8111-111111111111`).join(',')]){
  assert.throws(()=>client.send('selected',{...draft,dependencies}),/UUID|unique|at most 32/);
 }
 assert.equal(calls.length,0);assert.deepEqual(parseDependencies(` ${prerequisite.toUpperCase()} , 22222222-2222-4222-8222-222222222222 `),[prerequisite,'22222222-2222-4222-8222-222222222222']);
});

test('graph snapshots refresh and session replacement discards delayed graph responses',async()=>{
 let resolveGraph;let delay=false;
 const {backend,calls}=fixture((method)=>method==='coordination.graph'&&delay?new Promise(resolve=>{resolveGraph=resolve;}):undefined);
 const model=new CoordinationModel(backend);await model.setSession('selected');await model.inspect(item());
 assert.equal(model.getSnapshot().graph.nodes[0].readiness,'waiting');await model.reload();assert.equal(calls.filter(call=>call.method==='coordination.graph').length,2);
 delay=true;const pending=model.inspect(item());await waitFor(()=>resolveGraph);await model.setSession('replacement');resolveGraph(graph());await pending;
 assert.equal(model.getSnapshot().session,'replacement');assert.equal(model.getSnapshot().graph,null);model.dispose();
});

test('Ink dependency inspection exposes blockers, truncation and hidden prerequisites',async()=>{
 const {backend}=fixture(method=>method==='coordination.graph'?{...graph(),truncated:true,hidden_dependencies:true}:undefined);
 const ui=mount(React.createElement(CoordinationPanel,{backend,sessionId:'selected',active:true}));
 try{
  await waitFor(()=>ui.output().includes('blocked'));ui.input('\r');await waitFor(()=>ui.output().includes('original-conversation'));
  for(const label of ['Usage','Activity','Dependencies']){ui.input('v');await waitFor(()=>ui.output().includes(`${label} · v:next`));}
  assert.match(ui.output(),/prerequisite_incomplete/);assert.match(ui.output(),/Graph truncated at 100 nodes/);assert.match(ui.output(),/Some prerequisites are hidden/);
 }finally{ui.close();}
});


test('new draft sends validated prerequisites only after explicit submit',async()=>{
 const {backend,calls}=fixture();const model=new CoordinationModel(backend);await model.setSession('selected');model.newDraft();
 model.editDraft('message','Build UI after endpoint');model.editDraft('dependencies','malformed');await model.send();
 assert.match(model.getSnapshot().error,/UUID/);assert.equal(calls.filter(call=>call.method==='coordination.send').length,0);assert.ok(model.getSnapshot().draft);
 model.editDraft('dependencies',prerequisite);assert.equal(calls.filter(call=>call.method==='coordination.send').length,0);
 await model.send();assert.deepEqual(calls.find(call=>call.method==='coordination.send').params.depends_on,[prerequisite]);assert.equal(model.getSnapshot().draft,null);model.dispose();
});


test('contract pins validate canonical distinct UUIDs and bounded positive safe integer revisions before transport',()=>{
 const {backend,calls}=fixture();const client=new CoordinationClient(backend);
 const draft={to:'backend',message:'Wait for agreement',taskID:'',tokens:'',cost:'',key:'key'};
 for(const requiredContracts of ['bad@1',`${prerequisite}@0`,`${prerequisite}@-1`,`${prerequisite}@1.2`,`${prerequisite}@1e2`,`${prerequisite}@9007199254740992`,`${prerequisite}@1,${prerequisite.toUpperCase()}@2`,Array.from({length:33},(_,i)=>`${String(i).padStart(8,'0')}-1111-4111-8111-111111111111@1`).join(' '),'x'.repeat(4097)]) {
  assert.throws(()=>client.send('selected',{...draft,requiredContracts}),/UUID@revision|unique|at most 32|4096 bytes/);
 }
 assert.equal(calls.length,0);
 assert.deepEqual(parseContractPins(` ${prerequisite.toUpperCase()}@2, 22222222-2222-4222-8222-222222222222@9007199254740991 `),[{contract_id:prerequisite,revision:2},{contract_id:'22222222-2222-4222-8222-222222222222',revision:Number.MAX_SAFE_INTEGER}]);
 assert.deepEqual(parseContractPins(''),[]);
});

test('Ink contract gate graph renders acknowledgements, stale and deleted pins apart from test proof',async()=>{
 const message=item({required_contracts:[{contract_id:prerequisite,revision:2}]});
 const {backend}=fixture(method=>method==='coordination.message'?detail(message):method==='coordination.graph'?{...graph(message),nodes:[{message,readiness:'waiting',blockers:[],contract_blockers:[{contract_id:prerequisite,reason:'current_revision_not_acknowledged_by:backend',expected_revision:2},{contract_id:prerequisite,reason:'required_contract_revision_changed',expected_revision:2,actual_revision:3},{contract_id:prerequisite,reason:'required_contract_deleted',expected_revision:2}]}]}:undefined);
 const ui=mount(React.createElement(CoordinationPanel,{backend,sessionId:'selected',active:true}));
 try {
  await waitFor(()=>ui.output().includes('blocked'));ui.input('\r');await waitFor(()=>ui.output().includes(`${prerequisite}@2`));
  for(const label of ['Usage','Activity','Dependencies']){ui.input('v');await waitFor(()=>ui.output().includes(`${label} · v:next`));}
  assert.match(ui.output(),/Acknowledgement needed from backend/);assert.match(ui.output(),/Required contract revision changed/);assert.match(ui.output(),/current r3/);assert.match(ui.output(),/Required contract deleted/);assert.match(ui.output(),/Agreement and\s+completion are not test proof/);
 } finally {ui.close();}
});

test('new draft contract pins survive validation failure and submit together with prerequisites',async()=>{
 const {backend,calls}=fixture();const model=new CoordinationModel(backend);await model.setSession('selected');model.newDraft();
 model.editDraft('message','Build against accepted contract');model.editDraft('dependencies',prerequisite);model.editDraft('requiredContracts',`${prerequisite}@0`);await model.send();
 assert.match(model.getSnapshot().error,/UUID@revision/);assert.equal(calls.filter(call=>call.method==='coordination.send').length,0);
 model.editDraft('requiredContracts',`${prerequisite}@3`);await model.send();
 const sent=calls.find(call=>call.method==='coordination.send');assert.deepEqual(sent.params.required_contracts,[{contract_id:prerequisite,revision:3}]);assert.deepEqual(sent.params.depends_on,[prerequisite]);assert.equal(model.getSnapshot().draft,null);model.dispose();
});

test('Ink new request composer reviews contract pins before explicit send',async()=>{
 const {backend,calls}=fixture();const ui=mount(React.createElement(CoordinationPanel,{backend,sessionId:'selected',active:true}));
 const input=async(value,expected)=>{const mark=ui.output().length;ui.input(value);await waitFor(()=>ui.output().slice(mark).includes(expected));};
 try {
  await waitFor(()=>ui.output().includes('blocked'));await input('n','New request draft');
  for(const label of ['Task:','Token limit','USD limit','Prerequisite request UUIDs','Required contracts'])await input('\r',`› ${label}`);
  await input(`${prerequisite}@2`,`${prerequisite}@2`);await input('\r','› Message:');await input('Use agreed API','Use agreed API');await input('\r','Review above. Enter sends a new durable request.');
  assert.equal(calls.filter(call=>call.method==='coordination.send').length,0);
  ui.input('\r');await waitFor(()=>calls.some(call=>call.method==='coordination.send'));
  assert.deepEqual(calls.find(call=>call.method==='coordination.send').params.required_contracts,[{contract_id:prerequisite,revision:2}]);
 } finally {ui.close();}
});

test('Ink registration requires explicit repository scope selection during review',async()=>{
 let registered=false;const {backend,calls}=fixture(method=>{
  if(method==='coordination.peers')return {self:registered?{...self,project_id:'repository:fixture'}:null,peers:[]};
  if(method==='coordination.register'){registered=true;return {peer:self};}
 });
 const ui=mount(React.createElement(CoordinationPanel,{backend,sessionId:'selected',active:true}));
 const currentFrame=async(expected)=>{
  try{await waitFor(()=>ui.lastFrame().includes(expected)&&!ui.lastFrame().includes('Loading / saving…'));}
  catch(error){throw new Error(`${error.message}; expected settled ${JSON.stringify(expected)}; last frame:\n${ui.lastFrame()}`);}
 };
 const input=async(value,expected)=>{await React.act(async()=>{ui.input(value);});await currentFrame(expected);};
 try{
  await currentFrame('unregistered');await input('g','Agent name:');await input('frontend','Name: frontend');await input('\r','Role:');await input('UI','Role: UI');await input('\r','Review registration');
  assert.equal(calls.filter(c=>c.method==='coordination.register').length,0);
  await input('s','Repository worktrees (explicit opt-in)');ui.input('\r');await waitFor(()=>calls.some(c=>c.method==='coordination.register'));
  assert.deepEqual(calls.find(c=>c.method==='coordination.register').params,{session_id:'selected',name:'frontend',role:'UI',repository_scope:true});
 }finally{ui.close();}
});

const reviewMessage = () => item({id:prerequisite});
const reviewUsage = () => ({...usage,owner_peer_id:'self',revision:12,unknown_usage:0,unknown_price:0});
function reviewFixture(handle=()=>undefined) {
 return fixture((method,params,options)=>{
  const result=handle(method,params,options);if(result!==undefined)return result;
  if(method==='coordination.messages')return page([reviewMessage()]);
  if(method==='coordination.message')return detail(reviewMessage());
  if(method==='coordination.usage')return {usage:reviewUsage()};
  if(method==='coordination.review')return {message:{...reviewMessage(),state:'accepted',revision:8},usage:{...reviewUsage(),revision:13,limits:params.input.limits}};
 });
}

test('trusted review client whitelists exact revisions, explicit limits and note over authenticated HTTP',async()=>{
 const calls=[];const server=createServer(async(req,res)=>{let data='';for await(const chunk of req)data+=chunk;const rpc=JSON.parse(data);calls.push({rpc,authorization:req.headers.authorization});res.setHeader('content-type','application/json');res.end(JSON.stringify({jsonrpc:'2.0',id:rpc.id,result:{message:reviewMessage(),usage:reviewUsage()}}));});
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const client=new CoordinationClient(new BackendClient(`http://127.0.0.1:${server.address().port}`,'review-token'));
 const input={message_id:prerequisite,expected_revision:7,expected_usage_revision:12,review_id:'22222222-2222-4222-8222-222222222222',note:'Inspected earlier effects',limits:{tokens:900,cost_usd:2},requeue:true};
 try {
  await client.review('selected',{...input,owner_peer_id:'forged',actor:'forged',limits:{...input.limits,owner_peer_id:'forged'}});
  assert.equal(calls[0].authorization,'Bearer review-token');assert.deepEqual(calls[0].rpc.params,{session_id:'selected',input});
  for(const patch of [{note:' '},{note:'é'.repeat(1025)},{expected_revision:0},{expected_usage_revision:NaN},{review_id:'BAD'},{requeue:false},{limits:{tokens:1.5,cost_usd:1}},{limits:{tokens:1,cost_usd:Infinity}}])assert.throws(()=>client.review('selected',{...input,...patch}));
  assert.equal(calls.length,1);
 } finally {server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}
});

test('blocked budget review requires owner participation, explicit confirmation and preserves exact ambiguous retry',async()=>{
 let resolveReview;let submissions=0;
 const {backend,calls}=reviewFixture(method=>method==='coordination.review'&&++submissions===1?new Promise(resolve=>{resolveReview=resolve;}):undefined);
 const model=new CoordinationModel(backend);await model.setSession('selected');await model.inspect(reviewMessage());model.beginReview();
 assert.equal(model.getSnapshot().review.expectedUsageRevision,12);model.editReview('note','Reviewed filesystem effects');
 await model.submitReview();assert.match(model.getSnapshot().error,/confirm/);assert.equal(calls.filter(c=>c.method==='coordination.review').length,0);
 model.confirmReview();const pending=model.submitReview();const double=model.submitReview();await double;
 assert.equal(calls.filter(c=>c.method==='coordination.review').length,1);
 const frozen=model.getSnapshot().review.frozen;assert.ok(frozen);model.editReview('tokens','999');assert.equal(model.getSnapshot().review.tokens,'500');
 // Simulate an ambiguous transport rejection after the server might commit.
 resolveReview(Promise.reject(new Error('Connection lost after submission')));await pending;
 assert.equal(model.getSnapshot().review.frozen,frozen);assert.match(model.getSnapshot().error,/Connection lost/);
 await model.submitReview();const sent=calls.filter(c=>c.method==='coordination.review');assert.equal(sent.length,2);assert.deepEqual(sent[0].params,sent[1].params);
 assert.equal(model.getSnapshot().review,null);assert.equal(model.getSnapshot().detail.message.state,'accepted');assert.equal(model.getSnapshot().usage.revision,13);model.dispose();
 for(const patch of [{owner_peer_id:'peer'},{owner_peer_id:'self',revision:undefined}]){
  const denied=reviewFixture(method=>method==='coordination.usage'?{usage:{...reviewUsage(),...patch}}:undefined);const other=new CoordinationModel(denied.backend);await other.setSession('selected');await other.inspect(reviewMessage());other.beginReview();assert.equal(other.getSnapshot().review,null);other.dispose();
 }
 const denied=reviewFixture(method=>method==='coordination.message'?detail({...reviewMessage(),from:'third',to:'peer'}):undefined);const other=new CoordinationModel(denied.backend);await other.setSession('selected');await other.inspect(reviewMessage());other.beginReview();assert.equal(other.getSnapshot().review,null);other.dispose();
});

test('stale review keeps captured revisions and session replacement ignores its late response',async()=>{
 let resolveOld;let reviewSignal;const {backend,calls}=reviewFixture((method,params,options)=>{
  if(method==='coordination.review'&&params.session_id==='old'){reviewSignal=options.signal;return new Promise(resolve=>{resolveOld=resolve;});}
  if(method==='coordination.review')throw new Error('Revision conflict; refresh both request and usage');
 });
 const model=new CoordinationModel(backend);await model.setSession('selected');await model.inspect(reviewMessage());model.beginReview();model.editReview('note','Inspected');model.confirmReview();await model.submitReview();
 const body=model.getSnapshot().review.frozen;assert.match(model.getSnapshot().error,/Revision conflict/);assert.equal(body.expected_revision,7);assert.equal(body.expected_usage_revision,12);
 await model.setSession('old');await model.inspect(reviewMessage());model.beginReview();model.editReview('note','Old session inspection');model.confirmReview();const pending=model.submitReview();
 await model.setSession('new');await model.inspect(reviewMessage());model.beginReview();model.editReview('note','Current draft');const current=model.getSnapshot().review;
 resolveOld({message:{...reviewMessage(),state:'accepted'},usage:{...reviewUsage(),revision:999}});await pending;
 assert.equal(reviewSignal.aborted,true);assert.equal(model.getSnapshot().session,'new');assert.deepEqual(model.getSnapshot().review,current);assert.equal(model.getSnapshot().usage.revision,12);assert.equal(calls.filter(c=>c.method==='coordination.review').length,2);model.dispose();
});

test('Ink usage review warns about repeated effects, requires note and confirmation, manually retries frozen body',async()=>{
 let reviews=0;const {backend,calls}=reviewFixture(method=>{if(method==='coordination.review'&&++reviews===1)throw new Error('Ambiguous review response');});
 const ui=mount(React.createElement(CoordinationPanel,{backend,sessionId:'selected',active:true}));
 const input=async(value,expected)=>{const mark=ui.output().length;ui.input(value);await waitFor(()=>ui.output().slice(mark).includes(expected));await new Promise(resolve=>setTimeout(resolve,25));};
 try {
  await waitFor(()=>ui.output().includes('blocked'));await input('\r','original-conversation');await input('v','b:review budget');await input('b','Review blocked request budget');
  assert.match(ui.output(),/may repeat prior effects/);assert.equal(calls.filter(c=>c.method==='coordination.review').length,0);
  await input('\r','Review blocked request budget');await input('\r','Review note');await input('I checked prior edits','I checked prior edits');await input('\r','c:confirm');
  await input('\r','Explicitly confirm');assert.equal(calls.filter(c=>c.method==='coordination.review').length,0);
  await input('c','Confirmation: confirmed');ui.input('\r');await waitFor(()=>ui.output().includes('Ambiguous review response'));assert.match(ui.output(),/Submitted body locked/);
  const mark=ui.output().length;ui.input('999');await new Promise(resolve=>setTimeout(resolve,30));assert.equal(calls.filter(c=>c.method==='coordination.review').length,1);
  ui.input('\r');await waitFor(()=>calls.filter(c=>c.method==='coordination.review').length===2);await waitFor(()=>ui.output().slice(mark).includes('queued (accepted)'));
  const sent=calls.filter(c=>c.method==='coordination.review');assert.deepEqual(sent[0].params,sent[1].params);assert.equal(sent[0].params.input.note,'I checked prior edits');
 } finally {ui.close();}
});

test('Ink in-flight review blocks double submit and discards stale completion after session navigation',async()=>{
 let finish;const {backend,calls}=reviewFixture(method=>method==='coordination.review'?new Promise(resolve=>{finish=resolve;}):undefined);
 const element=sessionId=>React.createElement(CoordinationPanel,{backend,sessionId,active:true});const ui=mount(element('old'));
 const press=async(value,expected)=>{const mark=ui.output().length;ui.input(value);await waitFor(()=>ui.output().slice(mark).includes(expected));await new Promise(resolve=>setTimeout(resolve,20));};
 const openReview=async()=>{await press('\r','original-conversation');await press('v','b:review budget');await press('b','Review blocked request budget');};
 try {
  await waitFor(()=>ui.output().includes('blocked'));await openReview();await press('\r','Review note');await press('\r','Review note');await press('Checked old effects','Checked old effects');await press('\r','c:confirm');await press('c','Confirmation: confirmed');
  ui.input('\r');await waitFor(()=>calls.some(c=>c.method==='coordination.review'));ui.input('\r');await new Promise(resolve=>setTimeout(resolve,40));assert.equal(calls.filter(c=>c.method==='coordination.review').length,1);
  const mark=ui.output().length;ui.app.rerender(element('current'));await waitFor(()=>ui.output().slice(mark).includes('blocked'));await openReview();await press('\r','Review note');await press('\r','Review note');await press('Current review draft','Current review draft');
  const current=ui.output().length;finish({message:{...reviewMessage(),state:'accepted'},usage:{...reviewUsage(),revision:999}});await new Promise(resolve=>setTimeout(resolve,50));
  assert.doesNotMatch(ui.output().slice(current),/queued \(accepted\)|usage r999/);assert.equal(calls.filter(c=>c.method==='coordination.review').length,1);assert.equal(calls.find(c=>c.method==='coordination.review').options.signal.aborted,true);
  await press('\r','c:confirm');await press('c','Confirmation: confirmed');assert.match(ui.output().slice(current),/Current review draft/);
 } finally {ui.close();}
});

test('Ink stale revision rejection keeps locked reviewed revisions until user closes and refreshes',async()=>{
 const {backend,calls}=reviewFixture(method=>{if(method==='coordination.review')throw new Error('Revision conflict: refresh request and usage');});const ui=mount(React.createElement(CoordinationPanel,{backend,sessionId:'selected',active:true}));
 const press=async(value,expected)=>{const mark=ui.output().length;ui.input(value);await waitFor(()=>ui.output().slice(mark).includes(expected));await new Promise(resolve=>setTimeout(resolve,20));};
 try {
  await waitFor(()=>ui.output().includes('blocked'));await press('\r','original-conversation');await press('v','b:review budget');await press('b','Review blocked request budget');await press('\r','Review note');await press('\r','Review note');await press('Inspected stale work','Inspected stale work');await press('\r','c:confirm');await press('c','Confirmation: confirmed');
  await press('\r','Revision conflict');assert.match(ui.output(),/Submitted body locked/);assert.equal(calls.find(c=>c.method==='coordination.review').params.input.expected_usage_revision,12);
  const mark=ui.output().length;ui.input('r');await new Promise(resolve=>setTimeout(resolve,40));assert.equal(calls.filter(c=>c.method==='coordination.review').length,1);assert.equal(calls.filter(c=>c.method==='coordination.usage').length,1);
  await press('\u001b','Usage');assert.doesNotMatch(ui.output().slice(mark),/queued \(accepted\)/);
 } finally {ui.close();}
});

const accountingID='11111111-1111-4111-8111-111111111111';
const accountingTotals={input_tokens:2,output_tokens:1,cached_input_tokens:0,estimated_cost_usd:0.01,unknown_usage:1,unknown_price:1};
const accountingSnapshot=(revision=1)=>({message_id:accountingID,message_revision:revision,usage_revision:revision,scope:'task:fixture',owner_peer_id:'self',journal_fingerprint:String(revision).repeat(64),can_reconcile:true,reason:'',runs:[],run_count:0,runs_truncated:false,samples:[],sample_count:1,samples_truncated:false,observed:accountingTotals,contribution:accountingTotals,known_cost_floor_usd:0,previous:[],previous_count:0,previous_truncated:false});
const accountingPage={scope:'task:fixture',candidates:[{message_id:accountingID,message_revision:1,state:'blocked',pending_executions:0,unconfirmed_executions:0,unknown_usage:1,unknown_price:1,can_reconcile:true,reason:''}],next_cursor:'',truncated:false};

test('compiled reconciliation uses authenticated HTTP fixed methods with all explicit attestation fields',async()=>{
 const {UsageReconciliationClient}=await import('../dist/backend/usage-reconciliation.js');const calls=[];
 const server=createServer(async(req,res)=>{let raw='';for await(const chunk of req)raw+=chunk;const body=JSON.parse(raw);calls.push({...body,authorization:req.headers.authorization});res.setHeader('content-type','application/json');res.end(JSON.stringify({jsonrpc:'2.0',id:body.id,result:body.method.endsWith('candidates')?accountingPage:body.method.endsWith('reconcile')?{committed:false,conflict:true}:{snapshot:accountingSnapshot()}}));});await new Promise(r=>server.listen(0,'127.0.0.1',r));
 try{const client=new UsageReconciliationClient(new BackendClient(`http://127.0.0.1:${server.address().port}`,'accounting-token'));await client.candidates('selected',accountingID);await client.snapshot('selected',accountingID);const input={message_id:accountingID,expected_message_revision:1,expected_usage_revision:1,expected_journal_fingerprint:'1'.repeat(64),reconciliation_id:'22222222-2222-4222-8222-222222222222',input_tokens:20,output_tokens:3,cached_input_tokens:0,cost_usd:.1,usage_known:true,price_known:true,reason:'All attempts',evidence:'Operator retained logs',basis:'conservative_estimate',acknowledge_uncertainty:true};assert.deepEqual(await client.reconcile('selected',input),{committed:false,conflict:true});assert.deepEqual(calls.map(c=>c.method),['coordination.reconciliation.candidates','coordination.reconciliation','coordination.reconcile']);assert.ok(calls.every(c=>c.authorization==='Bearer accounting-token'&&c.params.session_id==='selected'));assert.deepEqual(calls[2].params,{session_id:'selected',input});assert.throws(()=>client.validate({...input,acknowledge_uncertainty:false}));}
 finally{server.closeAllConnections();await new Promise(r=>server.close(r));}
});

test('compiled reconciliation conflict preserves text, refresh requires new confirmation and ambiguous retry keeps UUID',async()=>{
 let revision=1,writes=0;const bodies=[];const {backend,calls}=fixture((method,params)=>{
 if(method==='coordination.reconciliation.candidates')return accountingPage;
 if(method==='coordination.reconciliation')return {snapshot:accountingSnapshot(revision)};
 if(method==='coordination.reconcile'){bodies.push(structuredClone(params.input));writes++;if(writes===1)return {committed:false,conflict:true};if(writes===2)throw new Error('Transport lost');return {committed:true,snapshot:accountingSnapshot(revision),usage,reconciliation:{reconciliation_id:params.input.reconciliation_id,provenance:'manual_attestation',basis:'conservative_estimate',evidence:params.input.evidence}};}
 });const model=new CoordinationModel(backend);await model.setSession('selected');await model.inspect(item());await model.beginReconciliation();
 for(const [field,value]of Object.entries({input:'20',output:'3',cached:'0',cost:'0.1',basis:'conservative_estimate',reason:'All attempts reviewed',evidence:'Operator log estimate'}))model.editReconciliation(field,value);model.confirmReconciliation();await model.submitReconciliation();assert.equal(model.getSnapshot().reconciliation.needsRefresh,true);assert.equal(model.getSnapshot().reconciliation.evidence,'Operator log estimate');await model.submitReconciliation();assert.equal(writes,1);
 revision=2;await model.refreshReconciliation();assert.equal(model.getSnapshot().reconciliation.confirmed,false);model.confirmReconciliation();await model.submitReconciliation();assert.notEqual(bodies[1].reconciliation_id,bodies[0].reconciliation_id);assert.equal(bodies[1].expected_journal_fingerprint,'2'.repeat(64));model.editReconciliation('evidence','forged');await model.submitReconciliation();assert.deepEqual(bodies[2],bodies[1]);assert.equal(model.getSnapshot().reconciliation.receipt.provenance,'manual_attestation');assert.equal(model.getSnapshot().reconciliation.historical,false);assert.equal(calls.filter(c=>['coordination.review','coordination.send'].includes(c.method)).length,0);model.dispose();
});

test('Ink usage inspector opens metadata reconciliation and labels manual all-attempt accounting',async()=>{
 const {backend,calls}=fixture(method=>method==='coordination.reconciliation.candidates'?accountingPage:method==='coordination.reconciliation'?{snapshot:accountingSnapshot()}:undefined);const ui=mount(React.createElement(CoordinationPanel,{backend,sessionId:'selected',active:true}));try{await waitFor(()=>ui.output().includes('blocked'));ui.input('\r');await waitFor(()=>ui.output().includes('original-conversation'));ui.input('v');await waitFor(()=>ui.output().includes('Unknown usage/price'));ui.input('u');await waitFor(()=>ui.output().includes('Reconcile uncertain usage'));assert.match(ui.output(),/manual attestation/i);assert.match(ui.output(),/all.*attempt/i);assert.equal(calls.filter(c=>c.method==='coordination.reconcile').length,0);}finally{ui.close();}
});

test('compiled terminal agreement view reads authenticated fixed RPC and exposes declared artifacts without granting effects',async()=>{
 const contract={id:prerequisite,owner_peer_id:'peer',revision:2,spec:{name:'Users API',endpoint:'GET /users',schema_version:'2',schema:{type:'object',required:['users']},workspace:'/isolated/backend',worktree:'/isolated/backend',owned_paths:['backend.cjs','users.json'],acceptance_criteria:['Actual integration check passes'],artifacts:[{name:'Server',path:'backend.cjs',media_type:'text/javascript',digest:'sha256:fixture'}],dependencies:[{contract_id:'22222222-2222-4222-8222-222222222222',revision:1}],compatibility_changes:[{kind:'breaking',from_version:'1',to_version:'2',description:'Role is required'}]}};
 const calls=[];const server=createServer(async(req,res)=>{let raw='';for await(const chunk of req)raw+=chunk;const body=JSON.parse(raw);calls.push({...body,authorization:req.headers.authorization});let result;if(body.method==='coordination.contract')result={result:[contract]};else if(body.method==='coordination.peers')result={self,peers:[self,recipient]};else if(body.method==='coordination.messages')result=page([item({required_contracts:[{contract_id:prerequisite,revision:1}]})]);else if(body.method==='coordination.message')result=detail(item({required_contracts:[{contract_id:prerequisite,revision:1}]}));else if(body.method==='coordination.graph')result=graph();else if(body.method==='coordination.usage')result={usage};else throw new Error('Unexpected method '+body.method);res.setHeader('Content-Type','application/json');res.end(JSON.stringify({jsonrpc:'2.0',id:body.id,result}));});await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const ui=mount(React.createElement(CoordinationPanel,{backend:new BackendClient(`http://127.0.0.1:${server.address().port}`,'contracts-fixture'),sessionId:'selected',active:true}));
 try{await waitFor(()=>ui.output().includes('blocked'));ui.input('\r');await waitFor(()=>ui.output().includes('Latest loaded update: blocked'));for(const expected of ['Usage','Activity','Dependencies','Agreements / artifacts']){ui.input('v');await waitFor(()=>ui.output().includes(expected));}await waitFor(()=>ui.output().includes('GET /users'));assert.match(ui.output(),/Pinned requirement: 1/);assert.match(ui.output(),/current r2/);for(let i=0;i<20;i++){ui.input('j');await new Promise(resolve=>setTimeout(resolve,5));}await waitFor(()=>ui.output().includes('Role is required'));assert.match(ui.output(),/Artifact: Server · backend.cjs/);assert.match(ui.output(),/Owns: users.json/);assert.match(ui.output(),/Actual integration check passes/);assert.match(ui.output(),/Contract dependency:/);assert.match(ui.output(),/Declarations do not prove files exist or tests pass/);const reads=calls.filter(c=>c.method==='coordination.contract');assert.equal(reads.length,1);assert.deepEqual(reads[0].params,{session_id:'selected',input:{action:'list'}});assert.equal(reads[0].authorization,'Bearer contracts-fixture');assert.equal(calls.filter(c=>['coordination.send','coordination.cancel','coordination.review'].includes(c.method)).length,0);
 }finally{ui.close();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}
});

test('published agreement transport cannot replace another chat after a delayed response',async()=>{
 let resolveContracts,signal;const {backend}=fixture((method,params,options)=>{if(method==='coordination.contract'){signal=options.signal;return new Promise(resolve=>{resolveContracts=resolve;});}});const model=new CoordinationModel(backend);await model.setSession('old');const pending=model.loadContracts();await waitFor(()=>!!resolveContracts);await model.setSession('new');resolveContracts({result:[{id:'stale'}]});await pending;assert.equal(signal.aborted,true);assert.equal(model.getSnapshot().session,'new');assert.equal(model.getSnapshot().contracts,null);model.dispose();
});

test('Ink coordination exposes working, waiting for input and all request outcomes without raw RPC inspection',async()=>{
 const live=state=>({state,source:'runtime_lease',observers:1,expires_at:new Date(Date.now()+60000).toISOString()});const requests=['accepted','started','blocked','completed','failed','cancelled'].map((state,index)=>item({id:'state-'+index,state,task_id:'Task '+state}));const {backend}=fixture(method=>method==='coordination.peers'?{self:{...self,presence:live('working')},peers:[{...self,presence:live('working')},{...recipient,presence:live('waiting_for_input')}]}:method==='coordination.messages'?page(requests):undefined);const ui=mount(React.createElement(CoordinationPanel,{backend,sessionId:'selected',active:true}));try{await waitFor(()=>ui.output().includes('Task cancelled'));for(const value of ['frontend: Working','backend: Waiting for input','queued · Task accepted','working · Task started','blocked · Task blocked','completed · Task completed','failed · Task failed','cancelled · Task cancelled','frontend → backend'])assert.ok(ui.output().includes(value),value);}finally{ui.close();}
});

test('Ink rapid view keys accumulate and agreement next/previous never opens a new-request draft',async()=>{
 const published=name=>({id:name,owner_peer_id:'peer',revision:1,spec:{name,endpoint:'GET /'+name,schema_version:'1',schema:{type:'object'},workspace:'/backend',owned_paths:[],acceptance_criteria:[],artifacts:[],dependencies:[],compatibility_changes:[]}});
 const {backend,calls}=fixture(method=>method==='coordination.contract'?{result:[published('first-agreement'),published('second-agreement')]}:undefined);const ui=mount(React.createElement(CoordinationPanel,{backend,sessionId:'selected',active:true}));try{
  await waitFor(()=>ui.output().includes('blocked'));ui.input('\r');await waitFor(()=>ui.output().includes('Overview · v:next view'));
  for(let i=0;i<4;i++){ui.input('v');await new Promise(resolve=>setImmediate(resolve));}await waitFor(()=>ui.output().includes('first-agreement · current r1'));assert.equal(calls.filter(call=>call.method==='coordination.contract').length,1);
  const before=ui.output().length;ui.input('n');await waitFor(()=>ui.output().slice(before).includes('second-agreement · current r1'));assert.doesNotMatch(ui.output().slice(before),/New request draft/);
  const previous=ui.output().length;ui.input('p');await waitFor(()=>ui.output().slice(previous).includes('first-agreement · current r1'));assert.doesNotMatch(ui.output(),/New request draft/);assert.equal(calls.filter(call=>call.method==='coordination.send').length,0);
 }finally{ui.close();}
});

test('committed registration-ready frame routes immediate n to new draft before passive input effects',async()=>{
 let registered=false,armed=false,queued=false,ui;const {backend,calls}=fixture(method=>method==='coordination.peers'?{self:registered?self:null,peers:[recipient]}:method==='coordination.register'?(registered=true,{peer:self}):undefined);
 ui=mount(React.createElement(CoordinationPanel,{backend,sessionId:'selected',active:true}),frame=>{
  if(armed&&!queued&&frame.includes('As frontend')&&frame.includes('Correspondence (1)')&&!frame.includes('Loading / saving…')){queued=true;queueMicrotask(()=>ui.input('n'));}
 });
 const expect=async text=>{try{await waitFor(()=>ui.lastFrame().includes(text));}catch(error){throw new Error(`${error.message}; queued=${queued}; last frame:\n${ui.lastFrame()}`);}};
 const input=async(value,text)=>{ui.input(value);await expect(text);await new Promise(resolve=>setTimeout(resolve,30));};
 try{await expect('unregistered');await input('g','Agent name:');await input('frontend','Name: frontend');await input('\r','Role:');await input('UI engineer','UI engineer');await input('\r','Review registration');armed=true;ui.input('\r');await expect('New request draft');assert.equal(queued,true);assert.equal(calls.filter(call=>call.method==='coordination.register').length,1);assert.equal(calls.filter(call=>call.method==='coordination.send').length,0);}finally{ui.close();}
});
