'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const os=require('node:os');
const http=require('node:http');
const crypto=require('node:crypto');
const {once}=require('node:events');
const {_electron}=require('playwright');

test('registered desktop team exchanges queued updates at safe boundaries and runs a fresh follow-up', {skip:process.env.OVERKILL_REQUIRE_DESKTOP_TEST!=='1',timeout:150000},async t=>{
 const root=await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(),'overkill-worker-messages-'))),workspace=path.join(root,'Workspace');await fs.mkdir(workspace);
 await fs.writeFile(path.join(workspace,'independent.cjs'),"const fs=require('node:fs');fs.writeFileSync('frontend-busy','yes');const timer=setInterval(()=>{if(fs.existsSync('release-frontend')){clearInterval(timer);console.log('INDEPENDENT_WORK_FINISHED');}},20);\n");
 await fs.writeFile(path.join(workspace,'integrated-check.cjs'),"const assert=require('node:assert/strict');const schema=require('./backend.json');assert.deepEqual(schema.fields,['id']);assert.equal(require('./frontend.js')(schema.example),'u-1');console.log('TEAM_INTEGRATION_PASSED');\n");
 const quote=value=>"'"+value.replaceAll("'","'\\''")+"'",command=name=>quote(process.execPath)+' '+name;
 let app,providerError,parentCalls=0,frontCalls=0,backCalls=0,followCalls=0,cancelCalls=0,frontID,questionID,report,followReport;
 let releaseFront;const frontGate=new Promise(resolve=>{releaseFront=resolve;});
 let releaseBackend;const backendGate=new Promise(resolve=>{releaseBackend=resolve;});
 const tool=(name,input)=>({tool_calls:[{id:crypto.randomUUID(),type:'function',function:{name,arguments:JSON.stringify(input)}}]});
 const provider=http.createServer(async(req,res)=>{
  try{
   let raw='';for await(const chunk of req)raw+=chunk;const body=JSON.parse(raw);assert.equal(req.headers.authorization,'Bearer fake-worker-message-key');
   const goal=body.messages.find(m=>m.role==='user'&&m.content?.startsWith('Delegated task:\n'))?.content;
   const lastTool=()=>JSON.parse(body.messages.filter(m=>m.role==='tool').at(-1).content);
   let message;
   if(goal?.includes('Explicit follow-up:')){
    followCalls++;
    if(followCalls===1)message=tool('fs',{action:'write',path:'frontend.js',content:'module.exports = user => user.id;\n// follow-up verified\n'});
    else if(followCalls===2)message=tool('shell',{command:command('integrated-check.cjs'),timeout_seconds:10});
    else{assert.equal(lastTool().exit_code,0);assert.match(lastTool().stdout,/TEAM_INTEGRATION_PASSED/);message={content:'Fresh follow-up changed the file and reran the actual integration check.'};}
   }else if(goal?.includes('FRONTEND_WORKER')){
    frontCalls++;
    assert.ok(body.tools.some(t=>t.function.name==='worker_messages'));assert.ok(!body.tools.some(t=>t.function.name==='delegate_task'));
    if(frontCalls===1)message=tool('worker_messages',{action:'send',to:'parent',kind:'question',content:'Which field should I display?',idempotency_key:'human-question'});
    else if(frontCalls===2)message=tool('worker_messages',{action:'send',to:'backend',kind:'question',content:'What fields does GET /users return?',idempotency_key:'schema-question'});
    else if(frontCalls===3){questionID=lastTool().id;message=tool('fs',{action:'write',path:'frontend.js',content:'module.exports = user => user.name;\n'});}
    else if(frontCalls===4)message=tool('terminal',{operation:'start',command:command('independent.cjs')});
    else if(frontCalls===5){const id=lastTool().id;await frontGate;message=tool('terminal',{operation:'read',id,wait_ms:1000});}
    else if(frontCalls===6){
     assert.match(body.messages.filter(m=>m.role==='tool').at(-1).content,/INDEPENDENT_WORK_FINISHED/);
     const context=body.messages.filter(m=>m.role==='user'&&m.content?.startsWith('Worker correspondence')).map(m=>m.content).join('\n');
     assert.match(context,/Human keeps scope: display the id field/);assert.match(context,/SCHEMA_READY: id:string/);
     message=tool('fs',{action:'edit',path:'frontend.js',old:'user.name',new:'user.id'});
    }else if(frontCalls===7)message=tool('shell',{command:command('integrated-check.cjs'),timeout_seconds:10});
    else if(frontCalls===8){assert.equal(lastTool().exit_code,0);assert.match(lastTool().stdout,/TEAM_INTEGRATION_PASSED/);message=tool('worker_messages',{action:'send',to:'parent',kind:'update',content:'Frontend integrated the schema and actual checks passed.',idempotency_key:'front-result'});}
    else message={content:'Frontend implemented the actual backend schema and verified integration.'};
   }else if(goal?.includes('BACKEND_WORKER')){
    backCalls++;
    if(backCalls===1){await backendGate;message=tool('worker_messages',{action:'inbox',wait_ms:1000});}
    else if(backCalls===2){const question=lastTool().messages.find(m=>m.kind==='question');assert.equal(question.id,questionID);message=tool('fs',{action:'write',path:'backend.json',content:JSON.stringify({fields:['id'],example:{id:'u-1'}})});}
    else if(backCalls===3)message=tool('worker_messages',{action:'send',to:'frontend',kind:'answer',content:'SCHEMA_READY: id:string',in_reply_to:questionID,idempotency_key:'schema-answer'});
    else if(backCalls===4)message=tool('worker_messages',{action:'send',to:'parent',kind:'update',content:'Backend schema published.',idempotency_key:'backend-result'});
    else message={content:'Backend published the schema and answered frontend.'};
   }else if(goal?.includes('CANCEL_WORKER')){
    cancelCalls++;assert.equal(cancelCalls,1,'cancelled worker must not receive a new processing turn');
    message=tool('shell',{command:'printf "%s" "$$" > cancelled-worker.pid; exec sleep 30',timeout_seconds:40});
   }else{
    parentCalls++;
    if(parentCalls===1)message=tool('delegate_task',{tasks:[{name:'frontend',goal:'FRONTEND_WORKER: implement the frontend, ask backend for the schema, and run real checks.'},{name:'backend',goal:'BACKEND_WORKER: publish the schema and answer frontend.'}]});
    else if(parentCalls===2){report=lastTool();assert.equal(report.results.length,2);assert.ok(report.results.every(r=>r.status==='completed'),JSON.stringify(report));frontID=report.results[0].worker_id;message={content:'Team exchange verified in the desktop.'};}
    else if(parentCalls===3)message=tool('delegate_task',{followup_of:frontID,goal:'Add a verification comment, preserving the interface, and rerun the real check.'});
    else if(parentCalls===4){followReport=lastTool();assert.equal(followReport.status,'completed');assert.notEqual(followReport.worker_id,frontID);message={content:'Fresh follow-up verified in the desktop.'};}
    else if(parentCalls===5)message=tool('delegate_task',{name:'cancel-worker',goal:'CANCEL_WORKER: wait at a cancellable command.'});
    else if(parentCalls===6)message=tool('worker_messages',{action:'list'});
    else{const state=lastTool();assert.ok(state.workers.length>=4);assert.ok(state.workers.every(w=>w.live===false));message={content:'Persisted worker history inspected without resuming old workers.'};}
   }
   const usage={prompt_tokens:40,completion_tokens:12};
   if(body.stream){const delta=message.tool_calls?{tool_calls:message.tool_calls.map((call,index)=>({index,...call}))}:{content:message.content};res.writeHead(200,{'Content-Type':'text/event-stream'});res.end(`data: ${JSON.stringify({choices:[{index:0,delta,finish_reason:message.tool_calls?'tool_calls':'stop'}],usage})}\n\ndata: [DONE]\n\n`);}
   else{res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({choices:[{message:{role:'assistant',...message},finish_reason:message.tool_calls?'tool_calls':'stop'}],usage}));}
  }catch(error){providerError=error;res.writeHead(500);res.end('Controlled worker messaging acceptance failed.');}
 });provider.listen(0,'127.0.0.1');await once(provider,'listening');
 t.after(async()=>{releaseBackend();releaseFront();if(app)await app.close();provider.closeAllConnections();await new Promise(resolve=>provider.close(resolve));await fs.rm(root,{recursive:true,force:true});});
 app=await _electron.launch({args:[path.join(__dirname,'fixtures/interactive.cjs'),`--overkill-profile=${path.join(root,'Profile')}`],timeout:30000});const window=await app.firstWindow(),errors=[];window.on('pageerror',e=>errors.push(e.message));
 await window.locator('#settings[open]').waitFor();await window.locator('#provider').selectOption('custom');await window.locator('#model').fill('worker-message-fixture');await window.locator('#base-url').fill(`http://127.0.0.1:${provider.address().port}/v1`);await window.locator('#api-key').fill('fake-worker-message-key');await window.locator('#settings-save').click();await window.locator('#settings[open]').waitFor({state:'hidden',timeout:45000});
 await app.evaluate(({dialog},folder)=>{dialog.showOpenDialog=async()=>({canceled:false,filePaths:[folder]});},workspace);await window.locator('#workspace').click();await window.locator('#new-session').click();
 await window.locator('#agent-name').fill('team-lead');await window.locator('#agent-role').fill('Coordinate coding children');await window.locator('#agent-register').click();await window.locator('#agent-identity').filter({hasText:'team-lead'}).waitFor();
 const session=(await window.evaluate(()=>window.overkill.listSessions())).sessions[0];
 const inventory=()=>window.evaluate(id=>window.overkill.workers({operation:'list',sessionID:id}),session.id);
 const inbox=()=>window.evaluate(id=>window.overkill.workers({operation:'inbox',sessionID:id}),session.id);
 const send=async content=>{await window.locator('#message').fill(content);await window.locator('#send').click();};
 const waitFile=async name=>{for(let i=0;i<200;i++){if(providerError)throw providerError;try{return await fs.readFile(path.join(workspace,name),'utf8');}catch(error){if(error.code!=='ENOENT')throw error;}await new Promise(resolve=>setTimeout(resolve,40));}throw new Error('No real worker effect: '+name+' '+await window.locator('#error').innerText());};
 await send('Build the frontend and backend with named workers and exchange schema updates.');await waitFile('frontend-busy');
 await window.locator('#sidebar-workers .sidebar-agent').filter({hasText:'frontend'}).waitFor();
 await window.locator('#sidebar-workers .sidebar-agent').filter({hasText:'frontend'}).click();
 const front=(await inventory()).workers.find(w=>w.name==='frontend');
 await window.locator('.worker-correspondence').filter({hasText:'Which field should I display?'}).getByRole('button',{name:'Reply',exact:true}).click();
 assert.equal(await window.locator('#worker-kind').inputValue(),'answer');
 await window.locator('#worker-content').fill('Human keeps scope: display the id field.');await window.locator('#worker-send').click();await window.locator('#worker-send-status').filter({hasText:'Queued'}).waitFor();
 releaseBackend();let queued;
 for(let i=0;i<200;i++){if(providerError)throw providerError;queued=await inbox();if(queued.messages.some(m=>m.kind==='answer'&&m.content.startsWith('SCHEMA_READY')))break;await new Promise(resolve=>setTimeout(resolve,40));}
 assert.equal(frontCalls,5,'queued messages must wait for the next model boundary while the worker and persistent command are busy');
 assert.ok(queued.messages.some(m=>m.kind==='answer'&&m.to===front.id&&m.state==='accepted'));
 assert.ok(queued.messages.some(m=>m.content.startsWith('Human keeps scope')&&m.state==='accepted'));
 await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-workers-busy-inbox.png')});
 await fs.writeFile(path.join(workspace,'release-frontend'),'yes');releaseFront();
 try{await window.getByText('Team exchange verified in the desktop.',{exact:true}).waitFor({timeout:20000});}catch(error){if(providerError)throw providerError;throw error;}
 await window.locator('#stop').waitFor({state:'hidden'});
 assert.match(await fs.readFile(path.join(workspace,'frontend.js'),'utf8'),/user.id/);
 await send('Follow up with the frontend worker and rerun its checks.');await window.getByText('Fresh follow-up verified in the desktop.',{exact:true}).waitFor({timeout:15000});await window.locator('#stop').waitFor({state:'hidden'});
 const after=await inventory();assert.equal(after.workers.find(w=>w.id===followReport.worker_id).followup_of,frontID);
 assert.match(await fs.readFile(path.join(workspace,'frontend.js'),'utf8'),/follow-up verified/);
 await send('Start a worker so I can cancel it while it runs.');const pid=Number(await waitFile('cancelled-worker.pid'));
 const cancellation=(await inventory()).workers.find(w=>w.name==='cancel-worker');
 await window.locator('#worker-recipient option[value="'+cancellation.id+'"]').waitFor({state:'attached'});await window.locator('#worker-recipient').selectOption(cancellation.id);
 await window.locator('#worker-content').fill('Queued before cancellation; do not replay.');await window.locator('#worker-send').click();await window.locator('#worker-send-status').filter({hasText:'Queued'}).waitFor();
 await window.locator('#stop').click();await window.locator('#stop').waitFor({state:'hidden'});
 let cancelled;
 for(let i=0;i<100;i++){cancelled=(await inventory()).workers.find(w=>w.id===cancellation.id);if(cancelled.status!=='running')break;await new Promise(resolve=>setTimeout(resolve,30));}
 assert.equal(cancelled.status,'interrupted');assert.throws(()=>process.kill(pid,0),{code:'ESRCH'});
 assert.equal((await inbox()).messages.find(m=>m.content.startsWith('Queued before cancellation')).state,'accepted');
 await assert.rejects(window.evaluate(({sessionID,to})=>window.overkill.workers({operation:'send',sessionID,to,kind:'update',content:'must not revive',idempotencyKey:'retired'}),{sessionID:session.id,to:cancellation.id}),/unavailable/);
 await window.locator('#emergency-stop').click();await window.locator('#emergency-stop-status').filter({hasText:'All agents in this app are stopped'}).waitFor();await window.locator('#settings-button').click();await window.locator('#settings-save').click();await window.locator('#settings[open]').waitFor({state:'hidden',timeout:45000});
 await send('Inspect persisted history without restarting any old worker.');await window.getByText('Persisted worker history inspected without resuming old workers.',{exact:true}).waitFor({timeout:15000});await window.locator('#stop').waitFor({state:'hidden'});
 assert.equal(cancelCalls,1);assert.equal(parentCalls,7);assert.ifError(providerError);assert.deepEqual(errors,[]);
 await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-workers-history.png')});
 const resources=process.env.OVERKILL_DESKTOP_RESOURCES||path.resolve(__dirname,'../../.cache/desktop-resources');const backendSHA256=crypto.createHash('sha256').update(await fs.readFile(path.join(resources,'bin/overkill'))).digest('hex');
 await fs.writeFile(path.resolve(__dirname,'../../.cache/worker-messaging-native-receipt.json'),JSON.stringify({backendSHA256,parentCalls,frontCalls,backCalls,followCalls,cancelCalls,report,followReport,queued,after,cancelled,scope:'Native macOS development app, real registered parent and coding children, real files/shell checks, queued sibling and human updates, explicit new follow-up attempt, cancellation and backend reconnect. Controlled local provider and disposable mock credential store; no paid subscription or installed-release claim.'},null,2));
});
