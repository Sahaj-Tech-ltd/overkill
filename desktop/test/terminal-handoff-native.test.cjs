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

for(const initiator of ['agent','human'])test('child server handoff survives cleanup and stops with the app ('+initiator+')', {skip:process.env.OVERKILL_REQUIRE_DESKTOP_TEST!=='1',timeout:130000},async t=>{
 const root=await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(),'overkill-terminal-handoff-'))),workspace=path.join(root,'Workspace');await fs.mkdir(workspace);
 const serverSource="const http=require('node:http'),fs=require('node:fs');const server=http.createServer((req,res)=>res.end('CHILD_SERVER'));server.listen(0,'127.0.0.1',()=>{fs.writeFileSync('server-port',String(server.address().port));fs.writeFileSync('server-pid',String(process.pid));console.log('SERVER_READY');});\n";
 await fs.writeFile(path.join(workspace,'verify-server.cjs'),"const http=require('node:http'),fs=require('node:fs'),assert=require('node:assert/strict');http.get('http://127.0.0.1:'+fs.readFileSync('server-port','utf8'),res=>{let text='';res.on('data',chunk=>text+=chunk);res.on('end',()=>{assert.equal(text,'CHILD_SERVER');console.log('SAME_SERVER_VERIFIED');});}).on('error',error=>{throw error;});\n");
 const quote=value=>"'"+value.replaceAll("'","'\\''")+"'",command=name=>quote(process.execPath)+' '+name;
 let releaseHuman;const humanGate=new Promise(resolve=>{releaseHuman=resolve;});
 let app,providerError,parentCalls=0,childCalls=0,terminalID,receipt,report;
 const tool=(name,input)=>({tool_calls:[{id:crypto.randomUUID(),type:'function',function:{name,arguments:JSON.stringify(input)}}]});
 const provider=http.createServer(async(req,res)=>{
  try{
   let raw='';for await(const chunk of req)raw+=chunk;const body=JSON.parse(raw);assert.equal(req.headers.authorization,'Bearer fake-handoff-key');
   const child=body.messages.some(m=>m.role==='user'&&m.content?.startsWith('Delegated task:\n'));
   const last=()=>JSON.parse(body.messages.filter(m=>m.role==='tool').at(-1).content);
   let message;
   if(child){
    childCalls++;
    assert.ok(body.tools.find(t=>t.function.name==='terminal').function.parameters.properties.operation.enum.includes('handoff'));
    if(childCalls===1)message=tool('fs',{action:'write',path:'server.cjs',content:serverSource});
    else if(childCalls===2)message=tool('terminal',{operation:'start',command:command('server.cjs')});
    else if(childCalls===3){terminalID=last().id;message=tool('terminal',{operation:'read',id:terminalID,wait_ms:1000});}
    else if(childCalls===4){assert.match(last().output,/SERVER_READY/);if(initiator==='human'){await humanGate;message=tool('terminal',{operation:'read',id:terminalID,cursor:0});}else message=tool('terminal',{operation:'handoff',id:terminalID,control_revision:last().terminal.control_revision});}
    else{if(initiator==='agent'){const transfer=last();assert.equal(transfer.success,true);assert.equal(transfer.handoff.state,'applied');assert.equal(transfer.terminal.owner.worker,'main');receipt=transfer.handoff;}else{assert.match(body.messages.filter(m=>m.role==='tool').at(-1).content,/ownership transferred/);assert.ok(body.messages.some(m=>m.role==='user'&&m.content?.includes('The human moved this terminal')),'child must receive the reserved handoff notice at its next boundary');}message={content:'Server ownership explicitly transferred to Main chat.'};}
   }else{
    parentCalls++;
    assert.ok(!body.tools.find(t=>t.function.name==='terminal').function.parameters.properties.operation.enum.includes('handoff'),'root cannot promote a terminal to another owner');
    if(parentCalls===1)message=tool('delegate_task',{name:'server-worker',goal:'SERVER_CHILD: start the real local server, wait for readiness, and explicitly hand it to Main chat so it survives your completion.'});
    else if(parentCalls===2){report=last();assert.equal(report.status,'completed');assert.equal((report.terminal_handoffs||[]).length,initiator==='agent'?1:0);if(initiator==='agent')assert.equal(report.terminal_handoffs[0].id,receipt.id);message=tool('worker_messages',{action:'list'});}
    else if(parentCalls===3){const recorded=last().terminal_handoffs[0].receipt;assert.equal(recorded.id,receipt.id);assert.equal(recorded.state,'applied');assert.equal(recorded.initiator,initiator);message=tool('terminal',{operation:'read',id:terminalID,cursor:0});}
    else if(parentCalls===4){assert.equal(last().terminal.owner.worker,'main');assert.equal(last().terminal.state,'running');message=tool('shell',{command:command('verify-server.cjs'),timeout_seconds:10});}
    else if(parentCalls===5){assert.equal(last().exit_code,0);assert.match(last().stdout,/SAME_SERVER_VERIFIED/);message={content:'Parent verified and owns the same child server.'};}
    else if(parentCalls===6)message=tool('worker_messages',{action:'list'});
    else{assert.equal(last().terminal_handoffs[0].receipt.id,receipt.id);assert.equal(last().terminal_handoffs[0].current_runtime,false);message={content:'Historical handoff inspected. No process resumed.'};}
   }
   const usage={prompt_tokens:30,completion_tokens:10};
   if(body.stream){const delta=message.tool_calls?{tool_calls:message.tool_calls.map((call,index)=>({index,...call}))}:{content:message.content};res.writeHead(200,{'Content-Type':'text/event-stream'});res.end(`data: ${JSON.stringify({choices:[{index:0,delta,finish_reason:message.tool_calls?'tool_calls':'stop'}],usage})}\n\ndata: [DONE]\n\n`);}
   else{res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({choices:[{message:{role:'assistant',...message},finish_reason:message.tool_calls?'tool_calls':'stop'}],usage}));}
  }catch(error){providerError=error;res.writeHead(500);res.end('Controlled terminal handoff acceptance failed.');}
 });provider.listen(0,'127.0.0.1');await once(provider,'listening');
 t.after(async()=>{releaseHuman();if(parentCalls!==7&&app){console.error({initiator,parentCalls,childCalls,providerError:providerError?.message});try{const page=await app.firstWindow();console.error({status:await page.locator('#live-terminal-status').innerText(),note:await page.locator('#live-terminal-note').innerText(),transcript:(await page.locator('#transcript').innerText()).slice(-5000),error:await page.locator('#error').innerText(),errors});await page.screenshot({path:path.resolve(__dirname,'../../.cache/handoff-failed-'+initiator+'.png')});}catch{}}if(app)await app.close();provider.closeAllConnections();await new Promise(resolve=>provider.close(resolve));await fs.rm(root,{recursive:true,force:true});});
 app=await _electron.launch({args:[path.join(__dirname,'fixtures/interactive.cjs'),`--overkill-profile=${path.join(root,'Profile')}`],timeout:30000});const window=await app.firstWindow(),errors=[];window.on('pageerror',e=>errors.push(e.message));
 await window.locator('#settings[open]').waitFor();await window.locator('#provider').selectOption('custom');await window.locator('#model').fill('handoff-fixture');await window.locator('#base-url').fill(`http://127.0.0.1:${provider.address().port}/v1`);await window.locator('#api-key').fill('fake-handoff-key');await window.locator('#settings-save').click();await window.locator('#settings[open]').waitFor({state:'hidden',timeout:45000});
 await app.evaluate(({dialog},folder)=>{dialog.showOpenDialog=async()=>({canceled:false,filePaths:[folder]});},workspace);await window.locator('#workspace').click();await window.locator('#new-session').click();
 await window.locator('#agent-name').fill('lead');await window.locator('#agent-role').fill('Own transferred servers');await window.locator('#agent-register').click();await window.locator('#agent-identity').filter({hasText:'lead'}).waitFor();
 const session=(await window.evaluate(()=>window.overkill.listSessions())).sessions[0];
 const send=async(content,answer)=>{await window.locator('#message').fill(content);await window.locator('#send').click();try{await window.getByText(answer,{exact:true}).waitFor({timeout:25000});}catch(error){if(providerError)throw providerError;throw new Error(error.message+' '+await window.locator('#error').innerText());}await window.locator('#stop').waitFor({state:'hidden'});};
 await window.locator('#message').fill('Delegate a server and keep it in Main chat after the child finishes.');await window.locator('#send').click();
 const running=(async()=>{try{await window.getByText('Parent verified and owns the same child server.',{exact:true}).waitFor({timeout:25000});}catch(error){if(providerError)throw providerError;throw error;}await window.locator('#stop').waitFor({state:'hidden'});})();running.catch(()=>{});
 if(initiator==='human'){
  await window.locator('#terminal-toggle').click();await window.waitForFunction(()=>!document.getElementById('terminal-keep').disabled);
  await window.locator('#terminal-keep').click();await window.locator('#live-terminal-status').filter({hasText:'Main chat · running'}).waitFor();
  const moved=await window.evaluate(id=>window.overkill.workers({operation:'list',sessionID:id}),session.id);receipt=moved.terminal_handoffs[0].receipt;releaseHuman();
 }
 await running;
 const pid=Number(await fs.readFile(path.join(workspace,'server-pid'),'utf8')),port=Number(await fs.readFile(path.join(workspace,'server-port'),'utf8'));
 process.kill(pid,0);assert.equal(await (await fetch(`http://127.0.0.1:${port}`)).text(),'CHILD_SERVER');
 const state=await window.evaluate(id=>window.overkill.workers({operation:'list',sessionID:id}),session.id);assert.equal(state.workers[0].status,'completed');assert.equal(state.terminal_handoffs[0].receipt.id,receipt.id);
 await window.locator('#task-workers .worker-card').filter({hasText:'server-worker'}).filter({hasText:'completed'}).waitFor();
 await window.locator('#worker-terminal-handoffs').filter({hasText:'server-worker → Main chat · terminal ownership transferred'}).waitFor();
 assert.equal(await window.locator('#sidebar-workers .sidebar-agent').count(),0,'completed child must leave the running-worker sidebar');
 await window.locator('#child-workers-panel').scrollIntoViewIfNeeded();
 if(!await window.locator('#terminal-dock').isVisible())await window.locator('#terminal-toggle').click();await window.locator('#live-terminal-status').filter({hasText:'Main chat · running'}).waitFor();assert.equal(await window.locator('#terminal-keep').isDisabled(),true);
 await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-terminal-handoff-'+initiator+'.png')});
 await window.locator('#emergency-stop').click();await window.locator('#emergency-stop-status').filter({hasText:'All agents in this app are stopped'}).waitFor();assert.throws(()=>process.kill(pid,0),{code:'ESRCH'});
 await assert.rejects(fetch(`http://127.0.0.1:${port}`));
 await window.locator('#settings-button').click();await window.locator('#settings-save').click();await window.locator('#settings[open]').waitFor({state:'hidden',timeout:45000});
 await send('Inspect the handoff history without restarting the process.','Historical handoff inspected. No process resumed.');
 const inventory=await window.evaluate(id=>window.overkill.terminal({operation:'list',sessionID:id}),session.id);assert.deepEqual(inventory.terminals,[]);
 assert.equal(childCalls,5);assert.equal(parentCalls,7);assert.ifError(providerError);assert.deepEqual(errors,[]);
 const resources=process.env.OVERKILL_DESKTOP_RESOURCES||path.resolve(__dirname,'../../.cache/desktop-resources');const backendSHA256=crypto.createHash('sha256').update(await fs.readFile(path.join(resources,'bin/overkill'))).digest('hex');
 await fs.writeFile(path.resolve(__dirname,'../../.cache/terminal-handoff-'+initiator+'-native-receipt.json'),JSON.stringify({initiator,backendSHA256,childCalls,parentCalls,terminalID,receipt,report,pid,port,state,inventory,scope:'Native macOS development app; registered parent/child, real Node HTTP server survives child completion, parent reads/tests same handle/server, stop kills it, reconnect reads history without replay. Controlled local provider and disposable mock credential store.'},null,2));
});
