const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');

test('late startup settings reads preserve the open dialog and entered credentials',async()=>{
 const source=fs.readFileSync(path.join(__dirname,'../src/renderer.js'),'utf8');
 const start=source.indexOf('async function showSettings() {');
 const end=source.indexOf("$('settings-button').addEventListener",start);
 assert.ok(start>=0 && end>start);
 const fields=new Map();
 const $=id=>{if(!fields.has(id))fields.set(id,{value:'',open:false,showModal(){this.open=true;}});return fields.get(id);};
 const pending=[];
 const context=vm.createContext({$,api:{state:()=>new Promise(resolve=>pending.push(resolve))}});
 vm.runInContext(source.slice(start,end),context);
 const first=context.showSettings();const late=context.showSettings();
 assert.equal(pending.length,2);
 pending[0]({settings:{provider:'openai',model:'saved'}});await first;
 $('model').value='draft model';$('base-url').value='http://127.0.0.1:1234/v1';$('api-key').value='test-only-secret';$('workspace-instructions').checked=true;
 pending[1]({settings:{provider:'openai',model:'stale'}});await late;
 assert.equal($('model').value,'draft model');
 assert.equal($('api-key').value,'test-only-secret');
 assert.equal($('base-url').value,'http://127.0.0.1:1234/v1');
 assert.equal($('workspace-instructions').checked,true);
 const reads=pending.length;await context.showSettings();assert.equal(pending.length,reads,'open dialog needs no background reload');
});
