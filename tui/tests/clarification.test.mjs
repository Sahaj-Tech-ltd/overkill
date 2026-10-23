import assert from 'node:assert/strict';
import test from 'node:test';
import React from 'react';
import {render,Text} from 'ink';
import {PassThrough,Writable} from 'node:stream';
import {useClarifyPoll} from '../dist/hooks/use-clarify-poll.js';

async function waitFor(check){const end=Date.now()+3000;while(!check()){if(Date.now()>end)throw new Error('clarification fixture timed out');await new Promise(done=>setTimeout(done,10));}}
function mount(Probe){const stdin=new PassThrough();Object.assign(stdin,{isTTY:true,setRawMode(){},ref(){},unref(){}});const stdout=new Writable({write(_data,_enc,done){done();}});Object.assign(stdout,{columns:100,rows:30,isTTY:false});const app=render(React.createElement(Probe),{stdin,stdout,stderr:stdout,debug:true,patchConsole:false,exitOnCtrlC:false});return()=>{app.unmount();stdin.end();};}

test('actual clarification hook fences cancel and later answers to the polled session and question',async()=>{
 const calls=[];let callback,request,setOpen;let polls=0;
 const backend={async call(method,params){calls.push({method,params});if(method==='clarify.poll'){polls++;return{session_id:polls===1?'first-session':'second-session',question_id:polls===1?'question-one':'question-two',question:'Choose?',choices:['yes','no']};}return{status:'answered'};}};
 function Probe(){const[open,change]=React.useState(false);setOpen=change;const show=React.useCallback((q,answer)=>{request=q;callback=answer;change(true);},[]);useClarifyPoll(backend,show,open);return React.createElement(Text,null,request?.question??'waiting');}
 const close=mount(Probe);
 try{
  await waitFor(()=>callback);const first=callback;first('',-1,true);setOpen(false);
  await waitFor(()=>callback!==first);
  callback('yes',0,false);
  first('late response',-1,false);
  const answers=calls.filter(c=>c.method==='agent.answer');
  assert.deepEqual(answers.map(c=>c.params),[
   {session_id:'first-session',question_id:'question-one',text:'',index:-1,cancel:true},
   {session_id:'second-session',question_id:'question-two',text:'yes',index:0,cancel:false},
   {session_id:'first-session',question_id:'question-one',text:'late response',index:-1,cancel:false},
  ]);
 }finally{close();}
});

test('actual clarification hook has one pending poll and discards its result after unmount',async()=>{
 let resolve,polls=0,shown=0;
 const backend={call(){polls++;return new Promise(done=>{resolve=done;});}};
 const show=()=>{shown++;};function Probe(){useClarifyPoll(backend,show,false);return React.createElement(Text,null,'waiting');}
 const close=mount(Probe);
 try{await waitFor(()=>resolve);await new Promise(done=>setTimeout(done,600));assert.equal(polls,1);}finally{close();}
 resolve({session_id:'one',question_id:'stale',question:'Old?',choices:[]});await new Promise(done=>setTimeout(done,30));assert.equal(shown,0);
});
