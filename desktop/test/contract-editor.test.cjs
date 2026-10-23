const {test}=require('node:test');
const assert=require('node:assert/strict');
const {create}=require('../src/contract-editor.js');
function fixture(api,session=()=>({id:'front'})) {
  class Element {
    constructor(tag){this.tag=tag;this.children=[];this.handlers={};this.dataset={};this.value='';this.open=false;}
    append(...children){for(const child of children){child.parent=this;this.children.push(child);}}
    replaceChildren(...children){this.children=[];this.append(...children);}
    remove(){this.parent.children=this.parent.children.filter(child=>child!==this);}
    setAttribute(){} focus(){} addEventListener(name,fn){(this.handlers[name]??=[]).push(fn);}
    showModal(){this.open=true;}close(){this.open=false;this.emit('close');}
    async emit(name){await Promise.all((this.handlers[name]||[]).map(fn=>fn({preventDefault(){}})));}
  }
  const body=new Element('body'), doc={body,createElement:tag=>new Element(tag)};
  const find=(node,id)=>node.id===id?node:node.children.map(child=>find(child,id)).find(Boolean);
  let saved=[];const editor=create({document:doc,api,session,onSaved:async result=>saved.push(result)});
  return {editor,saved,get:id=>find(body,id),form:()=>body.children[0].children[0]};
}
function fill(f,values={}) {
  for(const [key,value] of Object.entries({name:'Users',endpoint:'GET /users',version:'1',schema:'{"type":"object"}',criteria:'Returns users',paths:'frontend',...values}))f.get(`contract-${key}`).value=value;
}
const base={id:'contract',revision:1,owner_peer_id:'front',spec:{name:'Users',endpoint:'GET /users',schema_version:'1',schema:{type:'object'},workspace:'/project',worktree:'main',task_id:'users-task',owned_paths:['frontend'],acceptance_criteria:['Returns users'],artifacts:[{name:'Client',path:'frontend/client.js',media_type:'text/javascript',digest:'known'}],dependencies:[{contract_id:'backend',revision:1}],compatibility_changes:[]}};
test('editor publishes once after explicit submit and derives workspace through backend',async()=>{
  let resolve;const calls=[];const f=fixture({createContract:input=>{calls.push(input);return new Promise(done=>resolve=done);}});
  f.editor.open();fill(f);assert.equal(calls.length,0);
  const pending=f.form().emit('submit');await f.form().emit('submit');assert.equal(calls.length,1);assert.equal(f.get('contract-save').disabled,true);
  assert.equal(calls[0].sessionID,'front');assert.equal(calls[0].spec.workspace,undefined);assert.deepEqual(calls[0].spec.acceptance_criteria,['Returns users']);
  resolve({result:{id:'created',revision:1}});await pending;assert.equal(f.get('contract-editor').open,false);assert.equal(f.saved.length,1);
});
test('editing preserves metadata and dependency pins; schema changes require compatibility',async()=>{
  const calls=[];const f=fixture({updateContract:async input=>{calls.push(input);return {result:{id:'contract',revision:2}};}});
  f.editor.open(base,[{id:'backend',revision:2,spec:{name:'Backend'}}]);f.get('contract-version').value='2';
  await f.form().emit('submit');assert.equal(calls.length,0);assert.match(f.get('contract-editor-status').textContent,/compatibility/);
  f.get('contract-change-kind').value='compatible';f.get('contract-change').value='Schema shape retained.';await f.form().emit('submit');
  assert.equal(calls[0].revision,1);assert.deepEqual(calls[0].spec.dependencies,[{contract_id:'backend',revision:1}]);
  assert.equal(calls[0].spec.artifacts[0].digest,'known');assert.equal(calls[0].spec.task_id,'users-task');assert.equal(calls[0].spec.worktree,'main');
  assert.deepEqual(calls[0].spec.compatibility_changes,[{kind:'compatible',from_version:'1',to_version:'2',description:'Schema shape retained.'}]);
  assert.equal(base.spec.schema_version,'1','editing cannot mutate list snapshot');
});
test('failed or ambiguous saves retain draft and require fresh inspection; no implicit retry',async()=>{
  let calls=0;const f=fixture({createContract:async()=>{calls++;throw new Error('Connection lost');}});f.editor.open();fill(f);await f.form().emit('submit');
  assert.equal(f.get('contract-name').value,'Users');assert.match(f.get('contract-editor-status').textContent,/may have committed/);assert.equal(f.get('contract-save').disabled,true);assert.equal(calls,1);assert.equal(f.saved.length,0);await f.form().emit('submit');assert.equal(calls,1);
});
test('late save from a previous chat cannot close or label a new draft',async()=>{
  let sid='old',resolve;const f=fixture({createContract:()=>new Promise(done=>resolve=done)},()=>({id:sid}));f.editor.open();fill(f);const pending=f.form().emit('submit');
  f.editor.reset();sid='new';f.editor.open();fill(f,{name:'New chat draft'});resolve({result:{id:'old-save',revision:1}});await pending;
  assert.equal(f.get('contract-name').value,'New chat draft');assert.equal(f.get('contract-editor').open,true);assert.equal(f.saved.length,0);assert.equal(f.get('contract-save').disabled,false);
});
