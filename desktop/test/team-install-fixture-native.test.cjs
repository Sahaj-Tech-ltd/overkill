'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const os=require('node:os');
const http=require('node:http');
const {once}=require('node:events');
const {_electron}=require('playwright');

test('shared installed-team scenario performs real effects in the native development app',{skip:process.env.OVERKILL_REQUIRE_DESKTOP_TEST!=='1',timeout:100000},async t=>{
 const {WindowsTeamAcceptance}=await import('../../scripts/windows-team-acceptance.mjs');
 const root=await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(),'overkill-team-install-fixture-'))),workspace=path.join(root,'Workspace');await fs.mkdir(workspace);
 const team=new WindowsTeamAcceptance(workspace,process.execPath);
 team.scope='Native macOS development app validates shared installed-team provider/UI fixture with real files, HTTP and POSIX PTY effects. Controlled provider/mock credentials; no Windows or installed-app acceptance.';
 team.command=file=>"'"+process.execPath.replaceAll("'","'\\''")+"' '"+file.replaceAll("'","'\\''")+"'";
 let app,providerError;
 const provider=http.createServer(async(req,res)=>{
  try{let raw='';for await(const chunk of req)raw+=chunk;const body=JSON.parse(raw);assert.equal(req.headers.authorization,'Bearer fake-team-install-key');assert.ok(team.matches(body));const message=await team.respond(body),usage={prompt_tokens:30,completion_tokens:10};
   if(body.stream){const delta=message.tool_calls?{tool_calls:message.tool_calls.map((call,index)=>({index,...call}))}:{content:message.content};res.writeHead(200,{'Content-Type':'text/event-stream'});res.end(`data: ${JSON.stringify({choices:[{index:0,delta,finish_reason:message.tool_calls?'tool_calls':'stop'}],usage})}\n\ndata: [DONE]\n\n`);}
   else{res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({choices:[{message:{role:'assistant',...message},finish_reason:message.tool_calls?'tool_calls':'stop'}],usage}));}
  }catch(error){providerError=error;res.writeHead(500);res.end('Controlled team fixture failed.');}
 });provider.listen(0,'127.0.0.1');await once(provider,'listening');
 t.after(async()=>{if(app)await app.close();provider.closeAllConnections();await new Promise(resolve=>provider.close(resolve));await fs.rm(root,{recursive:true,force:true});});
 app=await _electron.launch({args:[path.join(__dirname,'fixtures/interactive.cjs'),`--overkill-profile=${path.join(root,'Profile')}`],timeout:30000});const page=await app.firstWindow(),errors=[];page.on('pageerror',error=>errors.push(error.message));
 await page.locator('#settings[open]').waitFor();await page.locator('#provider').selectOption('custom');await page.locator('#model').fill('team-install-fixture');await page.locator('#base-url').fill(`http://127.0.0.1:${provider.address().port}/v1`);await page.locator('#api-key').fill('fake-team-install-key');await page.locator('#settings-save').click();await page.locator('#settings[open]').waitFor({state:'hidden',timeout:45000});
 await app.evaluate(({dialog},folder)=>{dialog.showOpenDialog=async()=>({canceled:false,filePaths:[folder]});},workspace);await page.locator('#workspace').click();await page.locator('#new-session').click();const session=(await page.evaluate(()=>window.overkill.listSessions())).sessions[0];
 let receipt;try{receipt=await team.exercise(page,session,root);}catch(error){throw providerError||error;}assert.ifError(providerError);assert.deepEqual(errors,[]);
 await fs.copyFile(path.join(root,'windows-installed-team-terminal.png'),path.resolve(__dirname,'../../.cache/shared-team-fixture-native.png'));
 await fs.writeFile(path.resolve(__dirname,'../../.cache/shared-team-fixture-native-receipt.json'),JSON.stringify(receipt,null,2)+'\n');
});
