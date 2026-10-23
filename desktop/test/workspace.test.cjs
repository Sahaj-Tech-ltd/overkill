const {test} = require('node:test');
const assert = require('node:assert/strict');
const {usageSummary, stateLabel, blockerText} = require('../src/workspace.js');

test('workspace labels preserve queued, active, blocked and unknown distinctions', () => {
  assert.equal(stateLabel('accepted'), 'Queued'); assert.equal(stateLabel('started'), 'Working');
  assert.equal(stateLabel('blocked'), 'Needs review'); assert.equal(stateLabel('unconfirmed'), 'Unconfirmed');
  assert.equal(stateLabel('unknown-future-state'), 'Unknown');
  assert.equal(blockerText({reason:'current_revision_not_acknowledged_by:peer',expected_revision:1},new Map([['peer','frontend']])), 'Awaiting acknowledgement of revision 1 from frontend.');
  assert.ok(!blockerText({reason:'dependency_revision_changed',expected_revision:1}).includes('undefined'));
});

test('missing usage and crash journals cannot display a complete zero cost', () => {
  for (const missing of [{unknown_usage: 1}, {unknown_price: 1}, {unconfirmed_executions: 1}, {pending_executions: 1}]) {
    const summary = usageSummary({input_tokens: 8, output_tokens: 2, ...missing});
    assert.equal(summary.tokens, 10); assert.equal(summary.costText, 'Cost estimate incomplete');
  }
  const summary = usageSummary({input_tokens: 8, output_tokens: 2, estimated_cost_usd: 0.01, limits: {tokens: 100}, requires_review: true});
  assert.match(summary.tokenText, /10 reported.*100 limit/); assert.equal(summary.review, true);
});

function fakeDOM() {
  class Element {
    constructor(tag='div'){this.tagName=tag.toUpperCase();this.children=[];this.handlers={};this.text='';this.open=false;this.hidden=false;this.dataset={};}
    set textContent(value){this.text=String(value);this.children=[];} get textContent(){return this.text+this.children.map(child=>child.textContent).join('');}
    append(...children){this.children.push(...children);} replaceChildren(...children){this.text='';this.children=[...children];}
    addEventListener(name,fn){(this.handlers[name]??=[]).push(fn);} setAttribute(){} focus(){} showModal(){this.open=true;} close(){this.open=false;this.emit('close');}
    async emit(name,event={}){await Promise.all((this.handlers[name]||[]).map(fn=>fn(event)));}
  }
  const nodes=new Map();return {getElementById(id){if(!nodes.has(id))nodes.set(id,new Element());return nodes.get(id);},createElement(tag){return new Element(tag);}};
}
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b;});return {promise,resolve,reject};};

test('task A pagination failure cannot overwrite task B details', async()=>{
  const {create}=require('../src/workspace.js'), doc=fakeDOM(), old=deferred();
  const view=create({document:doc,session:()=>({id:'session'}),api:{
    peerTask:async input=>input.afterRevision ? old.promise : {message:{id:input.messageID,task_id:input.messageID,state:'started',payload:'work'},events:[],next_revision:1,has_more:true},
    taskUsage:async()=>({usage:{}}),
  }});
  await view.openTask({id:'A'}); const pagination=doc.getElementById('task-more').emit('click');
  await view.openTask({id:'B'});old.reject(new Error('A private diagnostic'));await pagination;
  assert.equal(doc.getElementById('task-title').textContent,'B');assert.equal(doc.getElementById('task-error').textContent,'');
});

test('older refresh for the same task cannot replace a newer snapshot',async()=>{
  const {create}=require('../src/workspace.js'),doc=fakeDOM(), old=deferred();let calls=0;
  const view=create({document:doc,session:()=>({id:'session'}),api:{
    peerTask:async()=>++calls===1?old.promise:{message:{id:'A',task_id:'latest',state:'completed'},events:[],next_revision:3},taskUsage:async()=>({usage:{}}),
  }});
  const first=view.openTask({id:'A'});await view.openTask({id:'A'});old.resolve({message:{id:'A',task_id:'old',state:'started'},events:[],next_revision:2});await first;
  assert.equal(doc.getElementById('task-title').textContent,'latest');assert.equal(doc.getElementById('task-state').textContent,'Completed');
});

const contract = revision => ({id:'contract',revision,owner_peer_id:'backend',spec:{name:'Users API',endpoint:'GET /users',schema_version:String(revision),workspace:'/project',owned_paths:['backend'],schema:{type:'object'}}});
const readiness = (revision,acknowledged=false)=>({contract_id:'contract',revision,peer_id:'frontend',ready:acknowledged,blockers:acknowledged?[]:[{contract_id:'contract',reason:'current_revision_not_acknowledged_by:frontend',expected_revision:revision}]});
const acknowledgement = revision => ({contract_id:'contract',revision,peer_id:'frontend',acknowledged_at:'2026-10-22T00:00:00Z'});
function contractNodes(doc) {
  const card=doc.getElementById('workspace-contracts').children.find(item=>item.dataset.contractId==='contract');
  const detail=card?.children.find(item=>item.tagName==='DETAILS');
  const content=detail?.children[1],actions=content?.children.find(item=>item.className==='contract-actions');
  return {card,detail,button:actions?.children[0],reload:actions?.children[1],status:actions?.children[2]};
}
async function contractFixture(api,session=()=>({id:'A'})) {
  const doc=fakeDOM(),view=require('../src/workspace.js').create({document:doc,session,api:{escalations:async()=>({escalations:[],next_cursor:0,has_more:false}),...api}});
  await view.refresh({registered:true});await doc.getElementById('team-tab-contracts').emit('click');
  const nodes=contractNodes(doc);nodes.detail.open=true;await nodes.detail.emit('toggle');
  return {doc,view,...nodes};
}

test('acknowledgement is explicit, survives polling and confirms the exact revision',async()=>{
  const pending=deferred();let calls=0,saved=false;
  const fixture=await contractFixture({contracts:async()=>({result:[contract(1)]}),contractReadiness:async()=>({result:readiness(1,saved)}),acknowledgeContract:async input=>{calls++;assert.deepEqual(input,{sessionID:'A',contractID:'contract',revision:1});await pending.promise;saved=true;return {result:acknowledgement(1)};}});
  const {view,doc,button,status}=fixture;assert.equal(calls,0);assert.equal(button.disabled,false);
  const click=button.emit('click');assert.equal(button.disabled,true);await button.emit('click');
  await view.refresh({registered:true});assert.equal(contractNodes(doc).button,button,'polling must preserve the active card');assert.equal(calls,1);
  pending.resolve();await click;assert.match(button.textContent,/Revision 1 acknowledged/);assert.equal(button.disabled,true);assert.match(status.textContent,/saved for this chat/);
  assert.match(doc.getElementById('workspace-contracts').textContent,/Agreement current/);
  assert.match(doc.getElementById('workspace-contracts').textContent,/does not prove.*checks passed/);
});

test('late acknowledgement cannot label a different session or newer revision acknowledged',async()=>{
  for(const target of ['session','revision']){
    const pending=deferred();let sid='A',revision=1;
    const {view,doc,button}=await contractFixture({contracts:async()=>({result:[contract(revision)]}),contractReadiness:async()=>({result:readiness(revision)}),acknowledgeContract:async()=>pending.promise},()=>({id:sid}));
    const click=button.emit('click');
    if(target==='session'){view.reset();sid='B';}else revision=2;
    await view.refresh({registered:true});const current=contractNodes(doc);current.detail.open=true;await current.detail.emit('toggle');
    pending.resolve({result:acknowledgement(1)});await click;
    assert.equal(current.button.disabled,false,target);assert.equal(current.button.textContent,`Acknowledge revision ${revision}`);assert.ok(!current.status.textContent.includes('saved for this chat'));
  }
});

test('failed acknowledgement remains visible through polling and permits an explicit retry',async()=>{
  let fail=true,saved=false,calls=0;
  const {view,doc,button,status}=await contractFixture({contracts:async()=>({result:[contract(1)]}),contractReadiness:async()=>({result:readiness(1,saved)}),acknowledgeContract:async()=>{calls++;if(fail)throw new Error('Reload before retrying');saved=true;return {result:acknowledgement(1)};}});
  await button.emit('click');assert.match(status.textContent,/Reload before retrying/);assert.equal(button.disabled,false);
  await view.refresh({registered:true});assert.equal(contractNodes(doc).button,button);assert.match(status.textContent,/Reload before retrying/);assert.equal(calls,1);
  fail=false;await button.emit('click');assert.equal(calls,2);assert.equal(button.disabled,true);assert.match(status.textContent,/saved for this chat/);
});

test('persisted acknowledgement is read from readiness and list outages can recover',async()=>{
  let outage=false;
  const {view,doc,button}=await contractFixture({contracts:async()=>{if(outage)throw new Error('Temporary list failure');return {result:[contract(1)]};},contractReadiness:async()=>({result:readiness(1,true)}),acknowledgeContract:async()=>{throw new Error('should not be invoked');}});
  assert.equal(button.disabled,true);await button.emit('click');
  outage=true;await view.refresh({registered:true});assert.match(doc.getElementById('workspace-contracts').textContent,/Temporary list failure/);
  outage=false;await view.refresh({registered:true});const restored=contractNodes(doc);assert.ok(restored.card);assert.equal(restored.detail.open,true);assert.match(restored.button.textContent,/acknowledged/);
});

test('a readiness response for a different revision prevents stale acknowledgement',async()=>{
  let calls=0;
  const {button,doc}=await contractFixture({contracts:async()=>({result:[contract(1)]}),contractReadiness:async()=>({result:readiness(2)}),acknowledgeContract:async()=>{calls++;}});
  assert.equal(button.disabled,true);await button.emit('click');assert.equal(calls,0);assert.match(doc.getElementById('workspace-contracts').textContent,/Agreement changed/);
});


test('Checks distinguishes observed confidence acceptance from actual passing tests',async()=>{
  const doc=fakeDOM(),view=require('../src/workspace.js').create({document:doc,session:()=>({id:'A'}),api:{verification:async()=>({total:0,passed:0,failed:0,persistence_available:true}),evaluation:async()=>({active:true,phase:'verifying',self_evaluation_available:true,iterative_evaluation:{status:'accepted',iteration:2,max_iterations:6}})}});
  await doc.getElementById('team-tab-checks').emit('click');
  const text=doc.getElementById('workspace-checks').textContent;
  assert.match(text,/Recorded self-evaluation: accepted.*iteration 2\/6/);assert.match(text,/Current activity: verifying/);assert.match(text,/not a test pass/);assert.match(text,/0 passed/);
});

test('runtime presence expires and never promotes registration or unknown observers to readiness',()=>{
  const {presenceLabel}=require('../src/workspace.js');const now=Date.parse('2026-10-22T20:00:00Z');
  const p={source:'runtime_lease',state:'working',observers:1,expires_at:'2026-10-22T20:00:20Z'};
  assert.equal(presenceLabel(p,now),'Working');assert.equal(presenceLabel({...p,state:'ready'},now),'Runtime idle');
  assert.equal(presenceLabel({...p,state:'waiting_for_input'},now),'Waiting for input');assert.equal(presenceLabel({...p,state:'busy'},now),'Finishing an operation');
  assert.equal(presenceLabel({...p,state:'multiple_runtimes',observers:2},now),'Multiple runtimes observed');
  for(const value of [undefined,{enabled:true},{...p,source:'client'},{...p,observers:0},{...p,expires_at:'invalid'},{...p,expires_at:'2026-10-22T20:00:00Z'},{...p,state:'online'}])assert.equal(presenceLabel(value,now),'Presence unknown');
});


test('Checks renders historical failed plan phases without promoting acceptance to test proof',async()=>{
 const doc=fakeDOM(),view=require('../src/workspace.js').create({document:doc,session:()=>({id:'A'}),api:{verification:async()=>({total:0,passed:0,failed:0}),evaluation:async()=>({plan_execution:{title:'Build workspace',current:false,status:'failed',phases:[{title:'Build client',status:'done'},{title:'Check API',status:'failed'},{title:'Publish',status:'pending'}]}})}});
 await doc.getElementById('team-tab-checks').emit('click');const text=doc.getElementById('workspace-checks').textContent;
 assert.match(text,/Previous execution.*failed/);assert.match(text,/1\/3 phases accepted/);assert.match(text,/Build client: Policy accepted/);assert.match(text,/Check API: Failed/);assert.match(text,/Publish: Not started/);assert.match(text,/recorded tests remain separate/);
});

test('dependency graph explains wait, privacy and truncation and ignores stale replies',async()=>{
 const {create}=require('../src/workspace.js'),doc=fakeDOM(),old=deferred();
 const graph=id=>({root_id:id,nodes:[{message:{id,task_id:`Task ${id}`,state:'accepted',depends_on:['backend-request']},readiness:'waiting',blockers:[{id:'backend-request',state:'started',reason:'waiting_for_completion'}]}],hidden_dependencies:true,truncated:true});
 const view=create({document:doc,session:()=>({id:'session'}),api:{peerTask:async({messageID})=>({message:{id:messageID,state:'accepted'},events:[]}),taskUsage:async()=>({usage:{}}),taskGraph:async({messageID})=>messageID==='A'?old.promise:graph(messageID)}});
 const a=view.openTask({id:'A'});await view.openTask({id:'B'});
 let text=doc.getElementById('task-dependencies').textContent;
 assert.match(text,/Waiting for prerequisites/);assert.match(text,/backend-request/);assert.match(text,/private to other agents/);assert.match(text,/ancestors are omitted/);
 old.resolve(graph('A'));await a;
 text=doc.getElementById('task-dependencies').textContent;assert.match(text,/Task B/);assert.ok(!text.includes('Task A'));
});

test('task agreement blockers distinguish acknowledgement and stale revision from test evidence',async()=>{
 const {create}=require('../src/workspace.js'),doc=fakeDOM();
 const view=create({document:doc,session:()=>({id:'session'}),api:{peerTask:async()=>({message:{id:'task',state:'accepted'},events:[]}),taskUsage:async()=>({usage:{}}),taskGraph:async()=>({root_id:'task',nodes:[{message:{id:'task',state:'accepted',required_contracts:[{contract_id:'agreement',revision:1}]},readiness:'waiting',contract_blockers:[{contract_id:'agreement',reason:'current_revision_not_acknowledged_by:peer',expected_revision:1},{contract_id:'agreement',reason:'required_contract_revision_changed',expected_revision:1,actual_revision:2}]}]})}});
 await view.openTask({id:'task'});const text=doc.getElementById('task-dependencies').textContent;assert.match(text,/Required agreement: agreement.*revision 1/);assert.match(text,/Awaiting acknowledgement/);assert.match(text,/Pinned revision 1 changed to 2/);assert.match(text,/does not.*prove tests passed/);
});

test('peer scope labels distinguish explicit repository membership from checkout identities',()=>{
 const {peerScopeLabel}=require('../src/workspace.js');
 assert.equal(peerScopeLabel({project_id:'repository:abc'}),'Repository worktrees');
 for(const peer of [null,{}, {project_id:'local:abc'}])assert.equal(peerScopeLabel(peer),'This checkout');
});

test('new contract stays disabled until the selected chat contract inventory is loaded',async()=>{
 const doc=fakeDOM(),pending=deferred();
 const view=require('../src/workspace.js').create({document:doc,session:()=>({id:'A'}),api:{escalations:async()=>({escalations:[]}),contracts:()=>pending.promise}});
 await view.refresh({registered:true});assert.equal(doc.getElementById('contract-new').disabled,true);
 const loading=doc.getElementById('team-tab-contracts').emit('click');assert.equal(doc.getElementById('contract-new').disabled,true);
 pending.resolve({result:[]});await loading;assert.equal(doc.getElementById('contract-new').disabled,false);
 view.reset();assert.equal(doc.getElementById('contract-new').disabled,true);
});
