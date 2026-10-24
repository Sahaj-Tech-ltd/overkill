const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const os=require('node:os');
const http=require('node:http');
const {once}=require('node:events');
const {_electron}=require('playwright');

test('native backend terminal persists across chat turns and supports human ownership and stop', {skip:process.env.OVERKILL_REQUIRE_DESKTOP_TEST!=='1',timeout:120000},async t=>{
 const root=await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(),'overkill-live-terminal-')));const workspace=path.join(root,'Workspace');await fs.mkdir(workspace);
 let app,providerError,calls=0,firstID,lastID;
 const provider=http.createServer(async(req,res)=>{
  try{
   let raw='';for await(const part of req)raw+=part;const body=JSON.parse(raw);assert.equal(req.headers.authorization,'Bearer fake-terminal-key');
   calls++;const terminal=body.tools.find(t=>t.function.name==='terminal');assert.ok(terminal,'missing real persistent terminal capability');assert.ok(terminal.function.parameters.properties.operation);
   let message;
   if(calls===1||calls===4||calls===6||calls===8||calls===10){message={tool_calls:[{id:'terminal-'+calls,type:'function',function:{name:'terminal',arguments:JSON.stringify({operation:'start',command:calls===1?'read value; stty size; printf \'NATIVE_PERSISTED:%s\\n\' "$value"':calls===8?'printf \'%s\' "$$" > live-terminal.pid; exec cat':'cat'})}}]};}
   else if(calls===2||calls===5||calls===7||calls===9||calls===11){const info=JSON.parse(body.messages.filter(m=>m.role==='tool').at(-1).content);assert.equal(info.state,'running');lastID=info.id;if(calls===2)firstID=info.id;message={content:calls===2?'Terminal is waiting for input.':calls===11?'Registered terminal is running.':calls===9?'Fourth terminal is running.':calls===7?'Third terminal is running.':'Second terminal is running.'};}
   else if(calls===12||calls===14){message={tool_calls:[{id:'guarded-write-'+calls,type:'function',function:{name:'shell',arguments:JSON.stringify({command:'printf permitted > effect-after-close.txt'})}}]};}
   else if(calls===13){assert.match(body.messages.filter(m=>m.role==='tool').at(-1).content,/background terminal reserves this workspace/);message={content:'Other agent correctly blocked by the live terminal.'};}
   else if(calls===15){const result=JSON.parse(body.messages.filter(m=>m.role==='tool').at(-1).content);assert.equal(result.exit_code,0);message={content:'Other agent wrote after terminal cleanup.'};}
   else {message={content:'The chat continued while the terminal stayed alive.'};}
   const delta=message.tool_calls?{tool_calls:message.tool_calls.map((call,index)=>({index,...call}))}:{content:message.content};res.writeHead(200,{'Content-Type':'text/event-stream'});res.end(`data: ${JSON.stringify({choices:[{index:0,delta,finish_reason:message.tool_calls?'tool_calls':'stop'}],usage:{prompt_tokens:30,completion_tokens:10}})}\n\ndata: [DONE]\n\n`);
  }catch(error){providerError=error;res.writeHead(500);res.end('Controlled persistent-terminal acceptance failed.');}
 });provider.listen(0,'127.0.0.1');await once(provider,'listening');
 t.after(async()=>{if(app)await app.close();provider.closeAllConnections();await new Promise(resolve=>provider.close(resolve));await fs.rm(root,{recursive:true,force:true});});
 app=await _electron.launch({args:[path.join(__dirname,'fixtures/interactive.cjs'),`--overkill-profile=${path.join(root,'Profile')}`],timeout:30000});const window=await app.firstWindow();const errors=[];window.on('pageerror',e=>errors.push(e.message));window.on('console',message=>{if(message.type()==='error')console.error('Renderer:',message.text());});
 await window.locator('#settings[open]').waitFor();await window.locator('#provider').selectOption('custom');await window.locator('#model').fill('terminal-fixture');await window.locator('#base-url').fill(`http://127.0.0.1:${provider.address().port}/v1`);await window.locator('#api-key').fill('fake-terminal-key');await window.locator('#settings-save').click();await window.locator('#settings[open]').waitFor({state:'hidden',timeout:45000});
 await app.evaluate(({dialog},folder)=>{dialog.showOpenDialog=async()=>({canceled:false,filePaths:[folder]});},workspace);await window.locator('#workspace').click();await window.locator('#new-session').click();
 const send=async(input,answer)=>{await window.locator('#message').fill(input);await window.locator('#send').click();try{await window.getByText(answer,{exact:true}).waitFor({timeout:20000});}catch(e){if(providerError)throw providerError;throw new Error(e.message+' '+await window.locator('#error').innerText());}await window.locator('#stop').waitFor({state:'hidden'});};
 await send('Start a terminal waiting for my input.','Terminal is waiting for input.');
 const session=(await window.evaluate(()=>window.overkill.listSessions())).sessions[0];
 const rpc=(method,params)=>app.evaluate((_,value)=>globalThis.workspaceFixtureRPC(value.method,value.params),{method,params:{session_id:session.id,...params}});
 await send('Continue this chat without replacing the terminal.','The chat continued while the terminal stayed alive.');
 let inventory=await rpc('terminal.list',{});assert.equal(inventory.terminals[0].id,firstID);assert.equal(inventory.terminals[0].state,'running');
 await assert.rejects(rpc('terminal.input',{id:firstID,data:'hello\n',control_revision:1,sequence:1}),/human has control|agent input is paused/);
 await window.locator('#terminal-toggle').click();
 await window.locator('#terminal-take-control').waitFor({state:'visible'});
 await window.waitForFunction(()=>!document.getElementById('terminal-take-control').disabled);
 await window.locator('#terminal-take-control').click();
 await window.locator('#live-terminal-status').filter({hasText:'You have control'}).waitFor();
 await window.locator('#terminal-return-control').click();
 await window.locator('#live-terminal-status').filter({hasText:'Agent has control'}).waitFor();
 await window.locator('#terminal-take-control').click();
 await window.locator('#live-terminal-status').filter({hasText:'You have control'}).waitFor();
 const terminalFrame=window.frameLocator('#live-terminal-frame');
 assert.equal(await terminalFrame.locator('body').evaluate(()=>typeof window.overkill),'undefined','sandboxed terminal must have no privileged bridge');
 await app.evaluate(({BrowserWindow})=>BrowserWindow.getAllWindows()[0].setSize(1220,850));
 try{await terminalFrame.locator('.xterm-helper-textarea').pressSequentially('hello',{timeout:5000});}catch(error){console.error({errors,frames:window.frames().map(f=>f.url()),frame:await terminalFrame.locator('body').innerHTML()});throw error;}
 await terminalFrame.locator('.xterm-helper-textarea').press('Enter');
 let output;for(let i=0;i<50;i++){output=await rpc('terminal.read',{id:firstID,cursor:0,wait_ms:100});if(output.terminal.state==='exited')break;await new Promise(resolve=>setTimeout(resolve,20));}
 assert.equal(output.terminal.exit_code,0);assert.match(output.output,/NATIVE_PERSISTED:hello/);
 const retry={id:firstID,control_revision:output.terminal.control_revision,sequence:output.terminal.input_sequence,data:'\r'};
 const receipts=await Promise.all(Array.from({length:8},()=>rpc('terminal.input',retry)));
 assert.ok(receipts.every(item=>item.success&&item.written===1&&item.outcome==='written'));
 assert.ok(receipts.every(item=>JSON.stringify(item)===JSON.stringify(receipts[0])));
 await assert.rejects(rpc('terminal.input',{...retry,data:'different'}),/different bytes/);
 if(retry.sequence>1)await assert.rejects(rpc('terminal.input',{...retry,sequence:retry.sequence-1}),/stale or out of order/);
 await assert.rejects(rpc('terminal.input',{...retry,control_revision:1,sequence:retry.sequence+1}),/control changed/);

 await window.locator('#live-terminal-status').filter({hasText:'exit 0'}).waitFor();
 await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-persistent-terminal-ui.png')});
 await send('Start a second terminal for the stop check.','Second terminal is running.');
 await window.locator('#live-terminal-select option[value="'+lastID+'"]').waitFor({state:'attached'});
 await window.locator('#live-terminal-select').selectOption(lastID);
 await window.waitForFunction(()=>!document.getElementById('terminal-end').disabled);
 await window.locator('#terminal-end').click();
 await window.locator('#live-terminal-status').filter({hasText:'closed'}).waitFor();
 await send('Start a third terminal for abort.','Third terminal is running.');
 await rpc('agent.abort',{});inventory=await rpc('terminal.list',{});assert.equal(inventory.terminals.find(t=>t.id===lastID).state,'closed');
 await send('Start another terminal before team registration.','Fourth terminal is running.');
 const ownedPID=Number(await fs.readFile(path.join(workspace,'live-terminal.pid'),'utf8'));assert.ok(Number.isSafeInteger(ownedPID)&&ownedPID>0);
 await rpc('coordination.register',{name:'frontend',role:'Build the frontend'});
 assert.throws(()=>process.kill(ownedPID,0),{code:'ESRCH'},'registration must retire the old background writer before coordinated ownership');
 await assert.rejects(rpc('terminal.read',{id:lastID,cursor:0}),/unavailable/);
 await send('Start a terminal in the registered agent.','Registered terminal is running.');
 const registeredID=lastID;
 await window.locator('#live-terminal-select option[value="'+registeredID+'"]').waitFor({state:'attached'});
 await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-registered-terminal-ui.png')});
 await window.locator('#new-session').click();
 await window.locator('#agent-name').fill('backend');await window.locator('#agent-role').fill('Build backend');await window.locator('#agent-register').click();
 await window.locator('#agent-identity').filter({hasText:'backend'}).waitFor();
 const other=(await window.evaluate(()=>window.overkill.listSessions())).sessions.find(item=>item.id!==session.id);
 await assert.rejects(rpc('terminal.read',{session_id:other.id,id:registeredID,cursor:0}),/unavailable|not found/);
 await send('Try writing while the other agent owns a background terminal.','Other agent correctly blocked by the live terminal.');
 await assert.rejects(fs.stat(path.join(workspace,'effect-after-close.txt')),{code:'ENOENT'});
 await rpc('terminal.close',{id:registeredID,control_revision:1});
 await send('Write now that the terminal has closed.','Other agent wrote after terminal cleanup.');
 assert.equal(await fs.readFile(path.join(workspace,'effect-after-close.txt'),'utf8'),'permitted');
 assert.ifError(providerError);assert.deepEqual(errors,[]);assert.equal(calls,15);
 await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-persistent-terminal-backend.png')});
 await fs.writeFile(path.resolve(__dirname,'../../.cache/persistent-terminal-native-receipt.json'),JSON.stringify({calls,inputRetriesReturnedOriginalReceipt:true,staleControlRejected:true,registrationRetiredOwnedProcess:true,registeredLaunchReserved:true,foreignWriterBlocked:true,writeAdmittedAfterCleanup:true,sessionID:session.id,firstID,lastID,output,inventory,scope:'Native development app and real bundled backend/PostgreSQL/PTY. Controlled provider and disposable mocked credential store. Human takeover/release, actual keyboard input, viewport resize, rendered output and End terminal exercised through the desktop UI. Registered launch reserves the workspace, denies a real competing shell effect and releases only after cleanup. Windows ConPTY and backend restart restoration remain open.'},null,2));
});
