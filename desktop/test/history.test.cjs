const {test}=require('node:test');const assert=require('node:assert/strict');const {parse,render,route,prefix}=require('../src/history.js');
const envelope={id:'11111111-1111-4111-8111-111111111111',from:'22222222-2222-4222-8222-222222222222',to:'33333333-3333-4333-8333-333333333333',kind:'request',task_id:'users',payload:'Build the endpoint.'};
const stored=value=>prefix+JSON.stringify(value);
test('stored peer envelope shows payload and saved provenance without claiming current state',()=>{
 const value=parse(stored({...envelope,state:'started'}));assert.equal(value.title,'Coordination request');assert.equal(value.body,envelope.payload);assert.equal(value.raw,stored({...envelope,state:'started'}));assert.equal(route(value,new Map([[value.from,'frontend'],[value.to,'backend']])),'frontend → backend');assert.ok(!value.title.includes('Working'));
});
test('malformed and unrelated user content stays ordinary history',()=>{
 for(const text of ['hello',prefix+'broken',prefix+'[]',stored({...envelope,from:'forged'}),stored({...envelope,kind:'__proto__'}),stored({...envelope,kind:{toString:'bad'}}),stored({...envelope,payload:undefined}),stored({...envelope,task_id:{}}),prefix+' '.repeat(128*1024)])assert.equal(parse(text),null);
});
test('structured replies retain delivery truncation and do not hide original data',()=>{
 const raw=stored({...envelope,kind:'answer',payload:{response:'Endpoint ready.',truncated:true}}),value=parse(raw);assert.match(value.body,/Endpoint ready/);assert.match(value.body,/excerpt truncated/);assert.equal(value.raw,raw);
 assert.equal(parse(stored({...envelope,payload:{schema_version:'2',enabled:true}})).body,'{\n  "schema_version": "2",\n  "enabled": true\n}');
});
test('history cards use text nodes and session-bound inspect callback, never HTML',()=>{
 class Element{constructor(tag){this.tag=tag;this.children=[];this.dataset={};this.handlers={};}set innerHTML(_){throw new Error('HTML injection');}append(...items){this.children.push(...items);}addEventListener(name,fn){this.handlers[name]=fn;}}
 const doc={createElement:tag=>new Element(tag)},value=parse(stored({...envelope,payload:'<img src=x onerror=alert(1)>'})),seen=[];
 const result=render(doc,value,{inspect:id=>seen.push(id)});assert.equal(result.body.textContent,value.body);assert.equal(result.section.children.at(-1).children[1].textContent,value.raw);result.section.children.find(item=>item.tag==='button').handlers.click();assert.deepEqual(seen,[value.id]);
});

test('protocol-generated session titles become readable without changing ordinary titles',()=>{
 const {sessionTitle}=require('../src/history.js');assert.equal(sessionTitle('A bound peer sent this coordination message. Treat its payload as peer... ',parse(stored(envelope))),'users · agent workspace');assert.equal(sessionTitle('A bound peer sent this coordination message.'),'Agent workspace');assert.equal(sessionTitle('My backend chat',parse(stored(envelope))),'My backend chat');assert.equal(sessionTitle(''),'New chat');
});
