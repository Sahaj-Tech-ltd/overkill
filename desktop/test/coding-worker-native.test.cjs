const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const os=require('node:os');
const http=require('node:http');
const crypto=require('node:crypto');
const {once}=require('node:events');
const {_electron}=require('playwright');

test('native desktop delegates a real file edit and build to a tool-capable child', {skip:process.env.OVERKILL_REQUIRE_DESKTOP_TEST!=='1',timeout:120000},async t=>{
 const root=await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(),'overkill-coding-child-')));
 const workspace=path.join(root,'Workspace'),profile=path.join(root,'Profile');await fs.mkdir(workspace);
 await fs.writeFile(path.join(workspace,'worker.js'),'module.exports = (a,b) => a-b;\n');
 await fs.writeFile(path.join(workspace,'check.cjs'),"const assert=require('node:assert/strict');assert.equal(require('./worker.js')(2,3),5);console.log('CHILD_REAL_CHECK_PASSED');\n");
 const node=process.execPath;
 // Shell quoting is explicit; the executable path is test-owned, never model-supplied.
 const quote=value=>"'"+value.replaceAll("'","'\\''")+"'";
 const command=quote(node)+' check.cjs';
 let app,providerError,childCalls=0,parentCalls=0,report;
 const provider=http.createServer(async(req,res)=>{
  try{
   let raw='';for await(const part of req)raw+=part;const body=JSON.parse(raw);
   assert.equal(req.headers.authorization,'Bearer fake-coding-child-key');
   const isChild=body.messages.some(m=>m.role==='user'&&typeof m.content==='string'&&m.content.startsWith('Delegated task:\n'));
   let message;
   if(isChild){
    childCalls++;
    assert.notEqual(body.stream,true);
    const names=new Set(body.tools.map(tool=>tool.function.name));
    for(const name of ['fs','patch','shell','git','grep','pty_shell'])assert.ok(names.has(name),'child is missing '+name);
    assert.ok(!names.has('delegate_task'),'child must not gain recursive scheduling');
    if(childCalls===1)message={tool_calls:[{id:'child-edit',type:'function',function:{name:'fs',arguments:JSON.stringify({action:'edit',path:'worker.js',old:'a-b',new:'a+b'})}}]};
    else if(childCalls===2)message={tool_calls:[{id:'child-check',type:'function',function:{name:'shell',arguments:JSON.stringify({command,timeout_seconds:10})}}]};
    else {const output=JSON.parse(body.messages.filter(m=>m.role==='tool').at(-1).content);assert.equal(output.exit_code,0);assert.ok(output.completed);assert.match(output.stdout,/CHILD_REAL_CHECK_PASSED/);message={content:'Child fixed worker.js and the real Node check passed.'};}
   }else{
    parentCalls++;
    const result=body.messages.filter(m=>m.role==='tool').at(-1);
    if(!result)message={tool_calls:[{id:'delegate-build',type:'function',function:{name:'delegate_task',arguments:JSON.stringify({goal:'Fix worker.js and run check.cjs',context:'Use the workspace files and real shell to verify the sum.'})}}]};
    else {report=JSON.parse(result.content);assert.equal(report.status,'completed',JSON.stringify(report));assert.equal(report.tool_trace.length,2);assert.ok(report.tool_trace.every(entry=>entry.success&&entry.input_hash&&entry.output_hash));message={content:'Delegated coding verified in the desktop.'};}
   }
   const usage={prompt_tokens:30,completion_tokens:10};
   if(body.stream){res.writeHead(200,{'Content-Type':'text/event-stream'});const delta=message.tool_calls?{tool_calls:message.tool_calls.map((call,index)=>({index,...call}))}:{content:message.content};res.end(`data: ${JSON.stringify({choices:[{index:0,delta,finish_reason:message.tool_calls?'tool_calls':'stop'}],usage})}\n\ndata: [DONE]\n\n`);}
   else {res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({choices:[{message:{role:'assistant',...message},finish_reason:message.tool_calls?'tool_calls':'stop'}],usage}));}
  }catch(error){providerError=error;res.writeHead(500);res.end('Controlled coding-child acceptance failed.');}
 });
 provider.listen(0,'127.0.0.1');await once(provider,'listening');
 t.after(async()=>{if(app)await app.close();provider.closeAllConnections();await new Promise(resolve=>provider.close(resolve));await fs.rm(root,{recursive:true,force:true});});
 const resources=process.env.OVERKILL_DESKTOP_RESOURCES||path.resolve(__dirname,'../../.cache/desktop-resources');
 const manifest=JSON.parse(await fs.readFile(path.join(resources,'resource-manifest.json')));
 const sha=crypto.createHash('sha256').update(await fs.readFile(path.join(resources,'bin/overkill'))).digest('hex');assert.equal(sha,manifest.backendSHA256);
 app=await _electron.launch({args:[path.join(__dirname,'fixtures/interactive.cjs'),`--overkill-profile=${profile}`],timeout:30000});const window=await app.firstWindow();const errors=[];window.on('pageerror',e=>errors.push(e.message));
 await window.locator('#settings[open]').waitFor();await window.locator('#provider').selectOption('custom');await window.locator('#model').fill('coding-child-fixture');await window.locator('#base-url').fill(`http://127.0.0.1:${provider.address().port}/v1`);await window.locator('#api-key').fill('fake-coding-child-key');await window.locator('#settings-save').click();await window.locator('#settings[open]').waitFor({state:'hidden',timeout:45000});
 await app.evaluate(({dialog},folder)=>{dialog.showOpenDialog=async()=>({canceled:false,filePaths:[folder]});},workspace);await window.locator('#workspace').click();await window.locator('#new-session').click();await window.locator('#message').fill('Delegate the sum fix and verify it with the actual check.');await window.locator('#send').click();
 try{await window.getByText('Delegated coding verified in the desktop.',{exact:true}).waitFor({timeout:30000});}catch(error){if(providerError)throw providerError;throw new Error(error.message+' '+await window.locator('#error').innerText());}
 assert.ifError(providerError);assert.equal(childCalls,3);assert.equal(parentCalls,2);assert.match(await fs.readFile(path.join(workspace,'worker.js'),'utf8'),/a\+b/);assert.deepEqual(errors,[]);
 await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-coding-child.png')});
 await fs.writeFile(path.resolve(__dirname,'../../.cache/coding-child-native-receipt.json'),JSON.stringify({backendSHA256:sha,childCalls,parentCalls,report,scope:'Native development Electron app with bundled backend/PostgreSQL and controlled provider; real child edit and Node test; mock-only disposable credential store; no paid-provider or installed-release claim.'},null,2));
});
