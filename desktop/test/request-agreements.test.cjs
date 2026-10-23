const {test}=require('node:test');
const assert=require('node:assert/strict');
const {parsePins,formatPins,choices,create}=require('../src/request-agreements.js');
const id='11111111-1111-4111-8111-111111111111';
test('agreement choices preserve stale and unavailable pins without silently repinning',()=>{
 assert.deepEqual(parsePins(`${id}@2`),[{contract_id:id,revision:2}]);
 assert.equal(formatPins([{contract_id:id,revision:2}]),`${id}@2`);
 assert.deepEqual(choices([{id,revision:3,spec:{name:'Users'}}],[{contract_id:id,revision:2}]),[{id,name:'Users',saved:2,current:3}]);
 assert.deepEqual(choices([],[{contract_id:id,revision:2}]),[{id,name:'Unavailable agreement',saved:2,current:0}]);
 for(const text of [id,`${id}@0`,`${id}@1.2`,`${id}@9007199254740992`,`${id}@1 ${id}@2`,'x'.repeat(4097)])assert.throws(()=>parsePins(text));
});
function documentFixture(){class Element{constructor(){this.children=[];this.handlers={};this.dataset={};this.open=false;this.value='';}append(...nodes){this.children.push(...nodes);}replaceChildren(...nodes){this.children=nodes;}addEventListener(name,fn){(this.handlers[name]??=[]).push(fn);}showModal(){this.open=true;}close(){this.open=false;this.emit('close');}async emit(name){await Promise.all((this.handlers[name]||[]).map(fn=>fn()));}}const nodes=new Map();return{getElementById(id){if(!nodes.has(id))nodes.set(id,new Element());return nodes.get(id);},createElement(){return new Element();}};}
test('picker requires explicit application and rejects old-session and failed list results',async()=>{
 const doc=documentFixture();let resolve,session='one',applied=null;
 const picker=create({document:doc,api:{contracts:()=>new Promise(done=>resolve=done)},session:()=>({id:session}),getPins:()=>`${id}@1`,onApply:pins=>applied=pins});
 let loading=picker.open();resolve([{id,revision:2,spec:{name:'Users'}}]);await loading;
 assert.equal(applied,null);const select=doc.getElementById('request-agreement-list').children[0].children[0];assert.equal(select.value,'1');select.value='2';await doc.getElementById('request-agreement-apply').emit('click');assert.deepEqual(applied,[{contract_id:id,revision:2}]);
 loading=picker.open();session='two';resolve([{id,revision:3,spec:{name:'Private old workspace'}}]);await loading;assert.equal(doc.getElementById('request-agreement-apply').disabled,true);
 picker.reset();assert.equal(doc.getElementById('request-agreements').open,false);
});
