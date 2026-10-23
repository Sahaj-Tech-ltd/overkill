const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const os=require('node:os');
const http=require('node:http');
const {once}=require('node:events');
const {_electron}=require('playwright');

test('native app records turns, reviews project skill proposals, reloads applied guidance and rolls it back', {
 skip:process.env.OVERKILL_REQUIRE_DESKTOP_TEST!=='1',timeout:120000,
},async t=>{
 const base=await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(),'overkill-evolution-')));
 const workspace=path.join(base,'Workspace'),profile=path.join(base,'Profile');await fs.mkdir(workspace);
 const body='# Project verification\nRun project checks and report observed results. NATIVE_EVOLVED_PROJECT_GUIDANCE';
 let application,window,providerError,generationCalls=0,normalCalls=0,expectApplied=false;
 const observations=[],pageErrors=[];
 const fixture=http.createServer(async(request,response)=>{
  try {
   let raw='';for await(const part of request)raw+=part;
   const input=JSON.parse(raw);assert.equal(request.headers.authorization,'Bearer fake-native-evolution-key');
   const prompt=input.messages.filter(m=>m.role==='system').map(m=>m.content).join('\n');
   let content;
   if(prompt.startsWith('Propose one reviewable project skill')){
    generationCalls++;assert.ok(!input.tools?.length);assert.equal(input.model,'evolution-fixture');
    assert.ok(input.max_tokens<=2048);assert.doesNotMatch(JSON.stringify(input.messages),/fake-native-evolution-key/);
    content=JSON.stringify({targetSkillName:'project-verification',draftType:'workflow',changeKind:'create',bodyOrPatch:body});
   }else{
    normalCalls++;assert.equal(prompt.includes(body),expectApplied,'only explicitly applied guidance may enter the model request');
    observations.push({call:normalCalls,guidance:prompt.includes(body)});content=`Observation ${normalCalls} completed.`;
   }
   const usage={prompt_tokens:12,completion_tokens:6};
   if(input.stream){response.writeHead(200,{'Content-Type':'text/event-stream'});response.end(`data: ${JSON.stringify({choices:[{index:0,delta:{content},finish_reason:'stop'}],usage})}\n\ndata: [DONE]\n\n`);}
   else{response.writeHead(200,{'Content-Type':'application/json'});response.end(JSON.stringify({choices:[{message:{role:'assistant',content},finish_reason:'stop'}],usage}));}
  }catch(error){providerError=error;response.writeHead(500);response.end('Controlled evolution fixture failed.');}
 });fixture.listen(0,'127.0.0.1');await once(fixture,'listening');
 t.after(async()=>{if(application)await application.close();fixture.closeAllConnections();await new Promise(resolve=>fixture.close(resolve));await fs.rm(base,{recursive:true,force:true});});
 async function launch(){application=await _electron.launch({args:[path.join(__dirname,'fixtures/interactive.cjs'),`--overkill-profile=${profile}`],timeout:30000});window=await application.firstWindow();window.on('pageerror',e=>pageErrors.push(e.message));}
 await launch();await window.locator('#settings[open]').waitFor();
 await window.locator('#provider').selectOption('custom');await window.locator('#model').fill('evolution-fixture');
 await window.locator('#base-url').fill(`http://127.0.0.1:${fixture.address().port}/v1`);await window.locator('#api-key').fill('fake-native-evolution-key');
 await window.locator('#settings-save').click();await window.locator('#settings[open]').waitFor({state:'hidden',timeout:45000});
 await application.evaluate(({dialog},selected)=>{dialog.showOpenDialog=async()=>({canceled:false,filePaths:[selected]});},workspace);
 await window.locator('#workspace').click();await window.locator('#new-session').click();
 const sessions=await window.evaluate(()=>window.overkill.listSessions());const session=sessions.sessions.find(s=>s.folder===workspace);assert.ok(session);
 async function submit(){const count=normalCalls+1;await window.locator('#message').fill('Observe this project and report the result.');await window.locator('#send').click();await window.locator('#transcript').getByText(`Observation ${count} completed.`,{exact:true}).last().waitFor({timeout:25000});await window.locator('#stop').waitFor({state:'hidden'});if(providerError)throw providerError;}
 for(let i=0;i<3;i++)await submit();
 await window.locator('#project-skills summary').click();await window.locator('#skill-generate').click();
 const candidate=window.locator('#skill-drafts article[data-status="candidate"]');await candidate.waitFor({timeout:25000});
 assert.equal(await candidate.locator('pre').textContent(),body);assert.equal(await candidate.getByRole('button',{name:'Apply reviewed skill',exact:true}).count(),0);
 await submit(); // Seeing a candidate does not activate it.
 await candidate.getByRole('button',{name:'Approve proposal',exact:true}).click();
 const accepted=window.locator('#skill-drafts article[data-status="accepted"]');await accepted.waitFor();
 await accepted.getByRole('button',{name:'Apply reviewed skill',exact:true}).click();await window.locator('#skill-drafts article[data-status="applied"]').waitFor();
 expectApplied=true;await submit();
 await application.close();application=null;await launch();
 await window.waitForFunction(()=>window.overkill.state().then(s=>s.ready),null,{timeout:45000});
 await window.locator(`#sessions button[data-session-id="${session.id}"]`).click();await submit();
 await window.locator('#project-skills summary').click();
 const applied=window.locator('#skill-drafts article[data-status="applied"]');await applied.waitFor();assert.equal(await applied.locator('pre').textContent(),body);
 await applied.getByRole('button',{name:'Roll back skill',exact:true}).click();await window.locator('#skill-drafts article[data-status="rolled_back"]').waitFor();
 expectApplied=false;await submit();assert.equal(generationCalls,1);assert.equal(normalCalls,7);assert.deepEqual(pageErrors,[]);if(providerError)throw providerError;
 const artifacts=path.resolve(__dirname,'../../.cache/evolution-native');await fs.mkdir(artifacts,{recursive:true});
 await fs.writeFile(path.join(artifacts,'receipt.json'),JSON.stringify({scope:'Actual Electron + prepared backend + private PostgreSQL + real HTTP/SSE adapter; controlled provider/fake safeStorage. No host Keychain or paid-provider test.',generationCalls,normalCalls,observations,explicitReviewRequired:true,appliedSurvivedRestart:true,rollbackRemovedGuidance:true,pageErrors},null,2));
 await window.screenshot({path:path.join(artifacts,'reviewed-rollback.png')});
});
