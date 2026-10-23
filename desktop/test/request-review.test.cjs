const {test}=require('node:test');const assert=require('node:assert/strict');const {create}=require('../src/request-review.js');
const message={id:'request-1',revision:3,kind:'request',state:'blocked',task_id:'users'};
const usage={revision:4,limits:{tokens:1,cost_usd:.5},input_tokens:30,output_tokens:10};
function fixture(api,session=()=>({id:'front'})){
 class Element{constructor(tag){this.tag=tag;this.children=[];this.handlers={};this.value='';this.open=false;}append(...items){this.children.push(...items);}setAttribute(){}focus(){}addEventListener(name,fn){(this.handlers[name]??=[]).push(fn);}showModal(){this.open=true;}close(){this.open=false;void this.emit('close');}async emit(name){await Promise.all((this.handlers[name]||[]).map(fn=>fn({preventDefault(){}})));}}
 const body=new Element('body'),doc={body,createElement:tag=>new Element(tag)},find=(node,id)=>node.id===id?node:node.children.map(child=>find(child,id)).find(Boolean);const saved=[];
 const editor=create({document:doc,api,session,reviewID:()=> '11111111-1111-4111-8111-111111111111',onSaved:result=>saved.push(result)});
 return {editor,saved,get:id=>find(body,id),submit:()=>body.children[0].children[0].emit('submit')};
}
function fill(f){f.get('request-review-tokens').value='1000';f.get('request-review-note').value='Reviewed partial files; safe to replay.';f.get('request-review-confirm').checked=true;}
test('request review needs explicit note and replay confirmation and submits once',async()=>{
 let resolve;const calls=[];const f=fixture({reviewRequest:input=>{calls.push(input);return new Promise(done=>resolve=done);}});f.editor.open(message,usage);
 await f.submit();assert.equal(calls.length,0);assert.match(f.get('request-review-status').textContent,/confirm replay/);fill(f);const pending=f.submit();await f.submit();assert.equal(calls.length,1);assert.equal(f.get('request-review-save').disabled,true);
 assert.deepEqual(calls[0],{sessionID:'front',messageID:'request-1',revision:3,usageRevision:4,reviewID:'11111111-1111-4111-8111-111111111111',note:'Reviewed partial files; safe to replay.',tokenLimit:1000,costLimit:.5,requeue:true});
 resolve({message:{...message,state:'accepted'},usage});await pending;assert.equal(f.saved.length,1);assert.equal(f.get('request-review').open,false);
});
test('ambiguous review retains frozen input and retry uses the exact same receipt key',async()=>{
 const calls=[];const f=fixture({reviewRequest:async input=>{calls.push(input);if(calls.length===1)throw new Error('Connection lost');return {message,usage};}});f.editor.open(message,usage);fill(f);await f.submit();assert.equal(calls.length,1);assert.equal(f.get('request-review-tokens').disabled,true);assert.match(f.get('request-review-status').textContent,/may have committed/);
 f.get('request-review-tokens').value='9000';await f.submit();assert.deepEqual(calls[1],calls[0]);assert.equal(f.saved.length,1);
});
test('late review result cannot close or label a new chat review',async()=>{
 let sid='old',resolve;const f=fixture({reviewRequest:()=>new Promise(done=>resolve=done)},()=>({id:sid}));f.editor.open(message,usage);fill(f);const pending=f.submit();f.editor.reset();sid='new';f.editor.open({...message,id:'new-request'},usage);resolve({message,usage});await pending;
 assert.equal(f.get('request-review').open,true);assert.equal(f.get('request-review-save').disabled,false);assert.equal(f.saved.length,0);assert.equal(f.get('request-review-note').value,'');
});
test('invalid numeric limits and multibyte notes cannot publish a review',async()=>{
 let calls=0;const f=fixture({reviewRequest:async()=>{calls++;}});f.editor.open(message,usage);fill(f);
 for(const value of ['', '-1','1.5','Infinity','1000000000001']){f.get('request-review-tokens').value=value;await f.submit();}
 f.get('request-review-tokens').value='100';f.get('request-review-note').value='界'.repeat(683);await f.submit();assert.equal(calls,0);
});

test('review summary identifies shared budget scope and incomplete pricing',()=>{
 const f=fixture({});f.editor.open(message,{...usage,scope:'task:users',estimated_cost_usd:0,unknown_price:1});
 assert.match(f.get('request-review-summary').textContent,/Cost estimate incomplete/);assert.match(f.get('request-review-summary').textContent,/all agents and requests in task:users/);assert.doesNotMatch(f.get('request-review-summary').textContent,/cost: \$0/i);
});
