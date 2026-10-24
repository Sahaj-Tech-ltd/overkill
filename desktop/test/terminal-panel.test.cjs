'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const crypto = require('node:crypto');
const {setImmediate:tick} = require('node:timers/promises');
const source = fs.readFileSync(require.resolve('../src/terminal-panel.js'),'utf8');
function fixture(invoke) {
  const nodes = new Map(), posted = [], messages = [], intervals = [];let now=Date.now();
  function node() { return {value:'',disabled:false,hidden:false,listeners:{},append(){},replaceChildren(){},setAttribute(){},addEventListener(name,fn){this.listeners[name]=fn;}}; }
  const document = {hidden:false,getElementById(id){if(!nodes.has(id))nodes.set(id,node());return nodes.get(id);},createElement:node};
  const frame = document.getElementById('live-terminal-frame');frame.contentWindow={postMessage(value){posted.push(value);}};
  const window={overkill:{terminal:invoke},addEventListener(name,fn){if(name==='message')messages.push(fn);}};
  vm.runInNewContext(source,{window,document,crypto,TextEncoder,atob,Date:{now:()=>now},setTimeout,clearTimeout,setInterval(fn){intervals.push(fn);}});
  const emit=(value,from=frame.contentWindow)=>{if(['input','binary','resize'].includes(value.type)&&value.inputEpoch===undefined)value={...value,inputEpoch:posted.filter(m=>m.type==='control').at(-1)?.inputEpoch};messages.forEach(fn=>fn({source:from,data:value}));};
  frame.listeners.load();
  const ready=()=>emit({type:'ready',token:posted.filter(m=>m.type==='init').at(-1).token});
  return {panel:window.overkillTerminals,nodes,posted,emit,ready,async poll(){now+=2000;intervals[0]();await tick();}};
}
const terminal={id:'31befbc2-c8e6-4d10-92c0-9226899457cd',owner:{worker:'backend'},state:'running',controller:'human',command:'cat',control_revision:2,input_sequence:0};

test('late terminal inventory cannot replace a newly selected chat',async()=>{
  let release;
  const f=fixture(()=>new Promise(resolve=>{release=resolve;}));
  f.panel.select({id:'old'});f.panel.connection(true);
  f.panel.select({id:'new'});release({available:true,terminals:[terminal]});await tick();
  assert.notEqual(f.nodes.get('live-terminal-status').textContent,'backend · running · You have control');
  assert.equal(f.nodes.get('terminal-take-control').disabled,true);
});

test('emulator cannot choose chat identity and queued keys cannot cross a chat switch',async()=>{
  const requests=[];
  const f=fixture(async input=>{requests.push(input);return input.operation==='input'?{success:true,sequence:input.sequence}:input.operation==='read'?{terminal,cursor:0,data:''}:{available:true,terminals:[terminal]};});
  f.panel.select({id:'first'});f.panel.connection(true);await tick();f.panel.visible(true);f.ready();await tick();
  const token=f.posted.filter(m=>m.type==='init').at(-1).token;
  f.emit({type:'input',token,controlRevision:2,data:'forged'},{});
  f.emit({type:'input',token:'old-token',controlRevision:2,data:'stale'});
  f.emit({type:'input',token,controlRevision:2,data:'discard on switch',sessionID:'victim'});
  f.panel.select({id:'second'});await tick();
  assert.equal(requests.filter(r=>r.operation==='input').length,0);
  await f.poll();f.ready();await tick();
  const next=f.posted.filter(m=>m.type==='init').at(-1).token;
  f.emit({type:'input',token:next,controlRevision:2,data:'safe',sessionID:'victim'});await tick();
  const input=requests.find(r=>r.operation==='input');assert.ok(input);assert.equal(input.sessionID,'second');assert.equal(input.data,'safe');
});

test('delayed emulator keys retain their original control revision and input epoch',async()=>{
 const requests=[],info={...terminal,owner:{...terminal.owner},control_revision:2,input_sequence:0};
 const copy=()=>JSON.parse(JSON.stringify(info));
 const f=fixture(async input=>{requests.push(input);return input.operation==='read'?{terminal:copy(),cursor:0,data:''}:input.operation==='input'?{success:true,sequence:input.sequence}:{available:true,terminals:[copy()]};});
 f.panel.select({id:'chat'});f.panel.connection(true);await tick();f.panel.visible(true);f.ready();await tick();
 const token=f.posted.filter(m=>m.type==='init').at(-1).token,oldEpoch=f.posted.filter(m=>m.type==='control').at(-1).inputEpoch;
 f.panel.visible(false);f.panel.visible(true);
 f.emit({type:'input',token,controlRevision:2,inputEpoch:oldEpoch,data:'delayed hidden key'});await tick();assert.equal(requests.filter(r=>r.operation==='input').length,0);
 info.control_revision=4;await f.poll();
 assert.equal(f.posted.filter(m=>m.type==='init').at(-1).token,token,'revision change does not rely on resetting the display');
 f.emit({type:'input',token,controlRevision:2,data:'retired control key'});await tick();assert.equal(requests.filter(r=>r.operation==='input').length,0);
 f.emit({type:'input',token,controlRevision:4,data:'fresh'});await tick();
 const writes=requests.filter(r=>r.operation==='input');assert.equal(writes.length,1);assert.equal(writes[0].data,'fresh');assert.equal(writes[0].controlRevision,4);assert.equal(writes[0].sequence,1);
});

test('lost input response pauses typing and explicit state check never resends old keys',async()=>{
 const writes=[],info={...terminal,owner:{...terminal.owner},control_revision:2,input_sequence:0};
 const copy=()=>JSON.parse(JSON.stringify(info));
 const f=fixture(async input=>{
  if(input.operation==='input'){writes.push(input);info.input_sequence=input.sequence;if(writes.length===1)throw new Error('response lost');return {success:true,sequence:input.sequence};}
  return input.operation==='read'?{terminal:copy(),cursor:0,data:''}:{available:true,terminals:[copy()]};
 });
 f.panel.select({id:'chat'});f.panel.connection(true);await tick();f.panel.visible(true);f.ready();await tick();
 const token=f.posted.filter(m=>m.type==='init').at(-1).token;
 f.emit({type:'input',token,controlRevision:2,data:'possibly delivered'});await tick();
 assert.match(f.nodes.get('live-terminal-note').textContent,/Input paused/);assert.equal(f.nodes.get('terminal-reconnect-input').disabled,false);
 f.emit({type:'input',token,controlRevision:2,data:'discarded while paused'});await tick();assert.equal(writes.length,1);
 await f.nodes.get('terminal-reconnect-input').listeners.click();
 f.emit({type:'input',token,controlRevision:2,data:'new intent'});await tick();
 assert.deepEqual(writes.map(item=>item.data),['possibly delivered','new intent']);assert.equal(writes[1].sequence,2);
});

test('lost interrupt response requires fresh state before any new keys',async()=>{
 const requests=[],info={...terminal,owner:{...terminal.owner},input_sequence:0};
 const copy=()=>JSON.parse(JSON.stringify(info));
 const f=fixture(async input=>{
  requests.push(input);
  if(input.operation==='interrupt'){info.input_sequence=input.sequence;throw new Error('interrupt response lost');}
  if(input.operation==='input')return {success:true,sequence:input.sequence};
  return input.operation==='read'?{terminal:copy(),cursor:0,data:''}:{available:true,terminals:[copy()]};
 });
 f.panel.select({id:'chat'});f.panel.connection(true);await tick();f.panel.visible(true);f.ready();await tick();
 f.nodes.get('terminal-interrupt').listeners.click();await tick();
 assert.match(f.nodes.get('live-terminal-note').textContent,/Input paused/);
 const token=f.posted.filter(m=>m.type==='init').at(-1).token;
 f.emit({type:'input',token,controlRevision:2,data:'blocked'});await tick();
 assert.equal(requests.filter(item=>item.operation==='input').length,0);
 await f.nodes.get('terminal-reconnect-input').listeners.click();
 f.emit({type:'input',token,controlRevision:2,data:'fresh'});await tick();
 assert.equal(requests.filter(item=>item.operation==='interrupt').length,1,'never resend uncertain Ctrl-C');
 assert.equal(requests.find(item=>item.operation==='input').sequence,2);
});
