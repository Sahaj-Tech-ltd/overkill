'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const os=require('node:os');
const http=require('node:http');
const crypto=require('node:crypto');
const {once}=require('node:events');
const {execFile}=require('node:child_process');
const {promisify}=require('node:util');
const {_electron}=require('playwright');

for(const boundary of ['accepted','admitted','acknowledged'])test('actual backend crash preserves '+boundary+' worker correspondence without replay', {skip:process.env.OVERKILL_REQUIRE_DESKTOP_TEST!=='1',timeout:100000},async t=>{
 const root=await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(),'overkill-worker-recovery-'))),workspace=path.join(root,'Workspace'),profile=path.join(root,'Profile');await fs.mkdir(workspace);
 const resources=process.env.OVERKILL_DESKTOP_RESOURCES||path.resolve(__dirname,'../../.cache/desktop-resources');
 let app,providerError,childCalls=0,parentCalls=0,messageID,workerID,before,after,killed,heldTransportClosed=false;
 let releaseSecond;const secondGate=new Promise(resolve=>{releaseSecond=resolve;});
 const entered=new Set();
 const tool=(name,input)=>({tool_calls:[{id:crypto.randomUUID(),type:'function',function:{name,arguments:JSON.stringify(input)}}]});
 const provider=http.createServer(async(req,res)=>{
  try{
   let raw='';for await(const chunk of req)raw+=chunk;const body=JSON.parse(raw);assert.equal(req.headers.authorization,'Bearer fake-worker-recovery-key');
   const isChild=body.messages.some(m=>m.role==='user'&&m.content?.startsWith('Delegated task:\n'));
   const last=()=>JSON.parse(body.messages.filter(m=>m.role==='tool').at(-1).content);
   let message;
   if(isChild){
    childCalls++;const step=childCalls;entered.add(step);
    if(step===1)message=tool('shell',{command:"printf 'ONE_EFFECT\\n' >> effect.txt",timeout_seconds:5});
    else if(step===2){
     assert.equal(last().exit_code,0);
     await Promise.race([secondGate,new Promise(resolve=>res.once('close',resolve))]);
     if(res.destroyed){heldTransportClosed=true;return;}
     message=tool('fs',{action:'read',path:'effect.txt'});
    }else if(step===3&&boundary==='acknowledged'){
     const copies=body.messages.filter(m=>m.role==='user'&&m.content?.startsWith('Worker correspondence')).map(m=>m.content).join('\n');assert.equal(copies.split('RECOVERY_UPDATE').length-1,1);
     message=tool('worker_messages',{action:'acknowledge',message_id:messageID});
    }else{
     if(step===3){const copies=body.messages.filter(m=>m.role==='user'&&m.content?.startsWith('Worker correspondence')).map(m=>m.content).join('\n');assert.equal(copies.split('RECOVERY_UPDATE').length-1,1);}
     await new Promise(resolve=>res.once('close',resolve));heldTransportClosed=true;return;
    }
   }else{
    parentCalls++;
    if(parentCalls===1)message=tool('delegate_task',{name:'recovery-worker',goal:'Write the one real effect, then inspect updates; wait for further input.'});
    else if(parentCalls===2)message=tool('worker_messages',{action:'list'});
    else if(parentCalls===3){const workers=last().workers;assert.equal(workers.length,1);assert.equal(workers[0].id,workerID);assert.equal(workers[0].live,false);assert.equal(workers[0].status,'running');message=tool('worker_messages',{action:'inbox'});}
    else if(parentCalls===4){const saved=last().messages.find(m=>m.id===messageID);assert.ok(saved);assert.equal(saved.state,boundary);message=tool('fs',{action:'read',path:'effect.txt'});}
    else{assert.match(body.messages.filter(m=>m.role==='tool').at(-1).content,/ONE_EFFECT/);message={content:'Recovered history inspected. Worker outcome is unconfirmed; nothing replayed.'};}
   }
   const usage={prompt_tokens:25,completion_tokens:8};
   if(body.stream){const delta=message.tool_calls?{tool_calls:message.tool_calls.map((call,index)=>({index,...call}))}:{content:message.content};res.writeHead(200,{'Content-Type':'text/event-stream'});res.end(`data: ${JSON.stringify({choices:[{index:0,delta,finish_reason:message.tool_calls?'tool_calls':'stop'}],usage})}\n\ndata: [DONE]\n\n`);}
   else{res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({choices:[{message:{role:'assistant',...message},finish_reason:message.tool_calls?'tool_calls':'stop'}],usage}));}
  }catch(error){providerError=error;res.writeHead(500);res.end('Controlled recovery acceptance failed.');}
 });provider.listen(0,'127.0.0.1');await once(provider,'listening');
 t.after(async()=>{releaseSecond();if(app)await app.close();provider.closeAllConnections();await new Promise(resolve=>provider.close(resolve));try{await promisify(execFile)(path.join(resources,'postgres/bin/pg_ctl'),['-D',path.join(profile,'backend/postgres'),'-m','immediate','-w','stop'],{timeout:10000});}catch{}await fs.rm(root,{recursive:true,force:true});});
 app=await _electron.launch({args:[path.join(__dirname,'fixtures/interactive.cjs'),`--overkill-profile=${profile}`],timeout:30000});const window=await app.firstWindow(),errors=[];window.on('pageerror',error=>errors.push(error.message));
 await window.locator('#settings[open]').waitFor();await window.locator('#provider').selectOption('custom');await window.locator('#model').fill('worker-recovery-fixture');await window.locator('#base-url').fill(`http://127.0.0.1:${provider.address().port}/v1`);await window.locator('#api-key').fill('fake-worker-recovery-key');await window.locator('#settings-save').click();await window.locator('#settings[open]').waitFor({state:'hidden',timeout:45000});
 await app.evaluate(({dialog},folder)=>{dialog.showOpenDialog=async()=>({canceled:false,filePaths:[folder]});},workspace);await window.locator('#workspace').click();await window.locator('#new-session').click();
 const session=(await window.evaluate(()=>window.overkill.listSessions())).sessions[0];
 const waitFor=async predicate=>{for(let i=0;i<300;i++){if(providerError)throw providerError;if(await predicate())return;await new Promise(resolve=>setTimeout(resolve,30));}throw new Error('Recovery fixture timed out: '+await window.locator('#error').innerText());};
 await window.locator('#message').fill('Delegate the single effect and wait for my update.');await window.locator('#send').click();await waitFor(()=>entered.has(2));
 before=await window.evaluate(id=>window.overkill.workers({operation:'list',sessionID:id}),session.id);workerID=before.workers[0].id;
 const sent=await window.evaluate(({sessionID,to})=>window.overkill.workers({operation:'send',sessionID,to,kind:'update',content:'RECOVERY_UPDATE: inspect the existing effect; do not repeat it.',idempotencyKey:'recovery-update'}),{sessionID:session.id,to:workerID});messageID=sent.id;assert.equal(sent.state,'accepted');
 if(boundary!=='accepted'){releaseSecond();await waitFor(()=>entered.has(boundary==='admitted'?3:4));}
 const queued=await window.evaluate(id=>window.overkill.workers({operation:'inbox',sessionID:id}),session.id);assert.equal(queued.messages.find(m=>m.id===messageID).state,boundary);
 assert.equal(await fs.readFile(path.join(workspace,'effect.txt'),'utf8'),'ONE_EFFECT\n');
 const previousCredentials=await app.evaluate(()=>globalThis.interactiveFixtureCredentials());
 killed=await app.evaluate(()=>globalThis.workspaceFixtureCrashBackend());assert.equal(killed.killed,true);
 await window.locator('#error').filter({hasText:'The backend stopped.'}).waitFor({timeout:15000});await waitFor(()=>heldTransportClosed);
 assert.equal((await window.evaluate(()=>window.overkill.state())).ready,false);
 const childCallsAtCrash=childCalls;assert.equal(parentCalls,1);
 await window.locator('#settings-button').click();await window.locator('#settings-save').click();await window.locator('#settings[open]').waitFor({state:'hidden',timeout:45000});
 const nextCredentials=await app.evaluate(()=>globalThis.interactiveFixtureCredentials());assert.notEqual(nextCredentials.token,previousCredentials.token);
 assert.equal(parentCalls,1);assert.equal(childCalls,childCallsAtCrash,'reconnect must not revive an old child');
 await window.locator('#message').fill('Inspect the saved worker history and existing file. Do not resume old work.');await window.locator('#send').click();
 try{await window.getByText('Recovered history inspected. Worker outcome is unconfirmed; nothing replayed.',{exact:true}).waitFor({timeout:20000});}catch(error){if(providerError)throw providerError;throw new Error(error.message+' '+await window.locator('#error').innerText());}
 await window.locator('#stop').waitFor({state:'hidden'});
 after=await window.evaluate(id=>window.overkill.workers({operation:'list',sessionID:id}),session.id);assert.equal(after.workers[0].live,false);assert.equal(childCalls,childCallsAtCrash);assert.equal(parentCalls,5);
 await window.locator('#task-workers .worker-card').filter({hasText:'outcome unconfirmed'}).waitFor();
 await assert.rejects(window.evaluate(({sessionID,to})=>window.overkill.workers({operation:'send',sessionID,to,kind:'update',content:'must not wake this old worker',idempotencyKey:'stale-target'}),{sessionID:session.id,to:workerID}),/unavailable/);
 assert.equal(await fs.readFile(path.join(workspace,'effect.txt'),'utf8'),'ONE_EFFECT\n');assert.ifError(providerError);assert.deepEqual(errors,[]);
 await window.locator('#child-workers-panel').scrollIntoViewIfNeeded();await window.screenshot({path:path.resolve(__dirname,'../../.cache/worker-recovery-'+boundary+'.png')});
 const backendSHA256=crypto.createHash('sha256').update(await fs.readFile(path.join(resources,'bin/overkill'))).digest('hex');
 await fs.writeFile(path.resolve(__dirname,'../../.cache/worker-recovery-'+boundary+'-receipt.json'),JSON.stringify({boundary,backendSHA256,parentCalls,childCalls,workerID,messageID,killed,before,after,queued,heldTransportClosed,scope:'Real macOS Electron development app and SIGKILL of its exact owned backend; persisted queued/admitted/acknowledged child messages and one file effect survive reconnect without restarting the child. Controlled local provider and disposable mock credentials. No private-context resume, terminal/broker recovery or installed-release claim.'},null,2));
});
