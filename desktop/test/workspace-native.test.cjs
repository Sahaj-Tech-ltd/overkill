const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const {execFileSync}=require('node:child_process');
const {once} = require('node:events');
const {_electron} = require('playwright');

test('desktop renders real task budgets, blockers, contracts and recorded checks', {skip: process.env.OVERKILL_REQUIRE_DESKTOP_TEST !== '1', timeout: 150000}, async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'overkill-workspace-ui-'));
  const profile = path.join(root, 'App Data'), workspace = path.join(root, 'Workspace'); await fs.mkdir(workspace);
  await fs.writeFile(path.join(workspace,'AGENTS.md'),'Desktop instruction marker. Use the workspace guidance.\n');
  const planFile=path.join(workspace,'Plan With Spaces.md');await fs.writeFile(planFile,'# Native loaded plan\n## Explain first phase\nNativePlanMarker first details.\n## Explain second phase\nNativePlanMarker second details.\n');
  const backendWorkspace=path.join(root,'Backend Checkout');
  const gitEnv=Object.fromEntries(Object.entries(process.env).filter(([key])=>!key.startsWith('GIT_')));
  Object.assign(gitEnv,{GIT_CONFIG_NOSYSTEM:'1',GIT_CONFIG_GLOBAL:os.devNull,GIT_AUTHOR_NAME:'Fixture',GIT_AUTHOR_EMAIL:'fixture@example.invalid',GIT_COMMITTER_NAME:'Fixture',GIT_COMMITTER_EMAIL:'fixture@example.invalid'});
  const git=(...args)=>execFileSync('git',['-C',workspace,'-c',`core.hooksPath=${os.devNull}`,...args],{env:gitEnv,stdio:'pipe'});
  git('init');git('add','AGENTS.md','Plan With Spaces.md');git('commit','-m','fixture');git('worktree','add','-b','backend-fixture',backendWorkspace);
  let application, release, block = true, requestCalls=0, instructionObserved=false;
  const fixture = http.createServer(async (req, res) => {
    const chunks = []; for await (const chunk of req) chunks.push(chunk); const body = JSON.parse(Buffer.concat(chunks));
    if(body.messages.some(message=>message.role==='system' && typeof message.content==='string' && message.content.includes('Desktop instruction marker.')))instructionObserved=true;
    const phase=body.messages.filter(message=>message.role==='user').at(-1)?.content?.match(/Execute ONLY phase (\d) of 2/);
    if(phase){
      const content=`Native phase ${phase[1]} complete.`;
      if(body.stream){res.writeHead(200,{'Content-Type':'text/event-stream'});res.end(`data: ${JSON.stringify({choices:[{index:0,delta:{content},finish_reason:'stop'}],usage:{prompt_tokens:10,completion_tokens:5}})}\n\ndata: [DONE]\n\n`);}
      else {res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({choices:[{message:{role:'assistant',content},finish_reason:'stop'}],usage:{prompt_tokens:12,completion_tokens:6}}));}
      return;
    }
    if(body.messages.some(message=>message.role==='user' && message.content==='Explain the review fixture.')) {
      if(body.stream){res.writeHead(200,{'Content-Type':'text/event-stream'});res.end(`data: ${JSON.stringify({choices:[{index:0,delta:{content:'Review fixture draft.'},finish_reason:'stop'}],usage:{prompt_tokens:10,completion_tokens:5}})}\n\ndata: [DONE]\n\n`);}
      else {res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({choices:[{message:{role:'assistant',content:'Review fixture revised.'},finish_reason:'stop'}],usage:{prompt_tokens:12,completion_tokens:6}}));}
      return;
    }
    const user = body.messages.filter(message => message.role === 'user').at(-1)?.content || '';
    if (user.startsWith('A bound peer sent this coordination message.')) {
      const message = JSON.parse(user.slice(user.indexOf('\n') + 1));
      if(message.kind==='request')requestCalls++;
      if (message.kind === 'request' && block) await new Promise(resolve => { release = resolve; });
      if(message.kind==='request' && message.payload==='Implement the users endpoint.' && !body.messages.some(item=>item.role==='tool' && item.tool_call_id==='isolated-write')){
        res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({choices:[{message:{role:'assistant',content:'',tool_calls:[{id:'isolated-write',type:'function',function:{name:'fs',arguments:JSON.stringify({action:'write',path:'backend-proof.txt',content:'Backend worktree only.\n'})}}]},finish_reason:'tool_calls'}],usage:{prompt_tokens:10,completion_tokens:5}}));return;
      }
      res.writeHead(200, {'Content-Type': 'application/json'}); res.end(JSON.stringify({model: body.model, choices: [{message: {role: 'assistant', content: message.kind === 'request' ? 'Endpoint is ready.' : 'Update received.'}, finish_reason: 'stop'}], usage: {prompt_tokens: 30, completion_tokens: 12}})); return;
    }
    const hasTool = body.messages.some(message => message.role === 'tool');
    const delta = hasTool ? {content: 'Saved workspace JSON.'} : {tool_calls: [{index: 0, id: 'json-write', type: 'function', function: {name: 'fs', arguments: JSON.stringify({action: 'write', path: 'project.json', content: '{"name":"workspace"}\n'})}}]};
    res.writeHead(200, {'Content-Type': 'text/event-stream'});
    res.end(`data: ${JSON.stringify({choices: [{index: 0, delta, finish_reason: hasTool ? 'stop' : 'tool_calls'}], usage: {prompt_tokens: 12, completion_tokens: 4}})}\n\ndata: [DONE]\n\n`);
  });
  fixture.listen(0, '127.0.0.1'); await once(fixture, 'listening');
  t.after(async () => {if(application){const active=application.windows()[0];if(active) await active.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-workspace-native-final.png')}).catch(()=>{});}release?.();if(application) await application.close();fixture.close();await fs.rm(root,{recursive:true,force:true});});
  application = await _electron.launch({args: [path.join(__dirname, 'fixtures/workspace.cjs'), `--overkill-profile=${profile}`], timeout: 30000});
  const window = await application.firstWindow();
  async function settingsConnected() {
    await window.waitForFunction(()=>!document.getElementById('settings').open || (!document.getElementById('settings-save').disabled && document.getElementById('settings-error').textContent),null,{timeout:45000});
    assert.equal(await window.locator('#settings').evaluate(node=>node.open),false,await window.locator('#settings-error').innerText());
  }

  const scriptErrors = []; window.on('pageerror', error => scriptErrors.push(error.message));
  t.after(async () => { assert.deepEqual(scriptErrors, []); });
  await window.locator('#settings[open]').waitFor();
  await window.locator('#provider').selectOption('custom'); await window.locator('#model').fill('workspace-fixture'); await window.locator('#base-url').fill(`http://127.0.0.1:${fixture.address().port}/v1`); await window.locator('#api-key').fill('workspace-test-key'); await window.locator('#workspace-instructions').check(); await window.locator('#settings-save').click();
  await settingsConnected();
  await application.evaluate(({dialog}, folder) => {dialog.showOpenDialog=async()=>({canceled:false,filePaths:[folder]});}, workspace);
  await window.locator('#workspace').click(); await window.locator('#new-session').click();
  await window.locator('#message').fill('Write the workspace JSON.'); await window.locator('#send').click();
  await window.getByText('Saved workspace JSON. Final revised answer.',{exact:true}).waitFor({timeout:30000}); await window.locator('#stop').waitFor({state:'hidden'});
  assert.equal(await fs.readFile(path.join(workspace,'project.json'),'utf8'),'{"name":"workspace"}\n');assert.equal(instructionObserved,true,'desktop instruction opt-in reaches actual provider prompt');
  const front = (await window.evaluate(()=>window.overkill.listSessions())).sessions[0];
  await window.locator('#agent-name').fill('frontend'); await window.locator('#agent-role').fill('Build UI'); await window.locator('#agent-repository-scope').check(); await window.locator('#agent-register').click(); await window.locator('#agent-identity').filter({hasText:'frontend'}).waitFor();
  await application.evaluate(({dialog},folder)=>{dialog.showOpenDialog=async()=>({canceled:false,filePaths:[folder]});},backendWorkspace);await window.locator('#workspace').click();
  await window.locator('#new-session').click(); const back = (await window.evaluate(()=>window.overkill.listSessions())).sessions.find(item=>item.id!==front.id);
  await window.locator('#agent-name').fill('backend'); await window.locator('#agent-role').fill('Build API'); await window.locator('#agent-repository-scope').check(); await window.locator('#agent-register').click(); await window.locator('#agent-identity').filter({hasText:'backend'}).waitFor();
  await window.locator(`#sessions button[data-session-id="${front.id}"]`).click(); await window.locator('#agent-identity').filter({hasText:'frontend'}).waitFor(); await window.locator('#agent-identity').filter({hasText:'frontend'}).waitFor();
  assert.equal(await window.locator('#peer-scope-heading').innerText(),'REPOSITORY WORKTREES');
  assert.match(await window.locator('.peer-card').filter({hasText:'backend'}).innerText(),/backend-fixture/);
  await window.locator('#peer-message').fill('Implement the users endpoint.'); await window.locator('.request-options summary').click(); await window.locator('#peer-task-name').fill('users-endpoint'); await window.locator('#peer-token-limit').fill('1000'); await window.locator('#peer-send').click();
  await window.locator('.peer-message .state-badge').filter({hasText:'Working'}).waitFor({timeout:15000});
  await window.locator('.peer-card').filter({hasText:'backend'}).getByText('Working',{exact:true}).waitFor({timeout:15000});
  // The backend provider is still deliberately blocked. Opening its chat
  // must read committed history immediately, without retrying turn admission.
  await window.locator(`#sessions button[data-session-id="${back.id}"]`).click();await window.locator('#agent-identity').filter({hasText:'backend'}).waitFor();
  const savedWhileBusy=await window.evaluate(id=>window.overkill.messages(id),back.id);assert.equal(savedWhileBusy.snapshot,'committed');assert.deepEqual(savedWhileBusy.messages,[]);
  await window.locator('#refresh').click();await window.waitForFunction(()=>!document.getElementById('refresh').disabled);assert.equal(await window.locator('#error').isVisible(),false);
  await window.locator(`#sessions button[data-session-id="${front.id}"]`).click();await window.locator('#agent-identity').filter({hasText:'frontend'}).waitFor();
  await window.locator('.peer-message').filter({hasText:'users-endpoint'}).getByRole('button',{name:'Inspect task',exact:true}).click();
  await window.locator('#task-state').filter({hasText:'Working'}).waitFor(); await window.locator('#task-usage').getByText('Cost estimate incomplete',{exact:true}).waitFor();
  const prerequisiteID = await window.locator('#task-reference').innerText();
  await window.locator('#task-close').click();
  await window.locator('#peer-message').fill('Use the completed endpoint for frontend integration.');
  await window.locator('#peer-task-name').fill('users-client');await window.locator('#peer-dependencies').fill(prerequisiteID);
  const callsBeforeDependency=requestCalls;await window.locator('#peer-send').click();
  await window.locator('.peer-message').filter({hasText:'users-client'}).getByRole('button',{name:'Inspect task',exact:true}).click();
  await window.locator('#task-dependencies').getByText('Waiting for prerequisites',{exact:true}).waitFor();
  assert.equal(requestCalls,callsBeforeDependency,'dependent request must not execute before its prerequisite completes');
  assert.match(await window.locator('#task-dependencies').innerText(),new RegExp(prerequisiteID));
  await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-task-dependencies.png')});
  assert.ok(release); block=false;release();
  await window.locator('#task-close').click(); await window.locator('.peer-message[data-state="completed"]').filter({hasText:'users-endpoint'}).getByText('Completed · request',{exact:true}).waitFor({timeout:15000});
  await window.locator('.peer-message').filter({hasText:'users-endpoint'}).filter({has:window.getByText('Completed · request',{exact:true})}).getByRole('button',{name:'Inspect task',exact:true}).click();
  await window.locator('#task-state').filter({hasText:'Completed'}).waitFor(); assert.match(await window.locator('#task-usage').innerText(),/reported.*1,000 limit/);
  await window.locator('#task-events').getByText('completed',{exact:true}).waitFor();
  await window.locator('.peer-message[data-state="completed"]').filter({hasText:'users-client'}).first().waitFor({timeout:15000});
  await window.locator('.peer-card').filter({hasText:'backend'}).getByText('Runtime idle',{exact:true}).waitFor({timeout:15000});
  await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-task-inspector.png')}); await window.locator('#task-close').click();
  assert.equal(await fs.readFile(path.join(backendWorkspace,'backend-proof.txt'),'utf8'),'Backend worktree only.\n');
  await assert.rejects(fs.access(path.join(workspace,'backend-proof.txt')),{code:'ENOENT'});
  // A saved coordination turn is a readable payload card, with original
  // envelope disclosure and an authenticated link to current task state.
  await window.locator(`#sessions button[data-session-id="${back.id}"]`).click();await window.locator('#agent-identity').filter({hasText:'backend'}).waitFor();
  assert.equal(await window.locator('#chat-title').innerText(),'users-endpoint · agent workspace');
  const historyCard=window.locator(`#transcript .coordination[data-coordination-id="${prerequisiteID}"]`);await historyCard.getByRole('heading',{name:'Coordination request',exact:true}).waitFor();
  await historyCard.getByText('Implement the users endpoint.',{exact:true}).waitFor();assert.match(await historyCard.locator('.coordination-route').innerText(),/frontend.*backend/);
  assert.equal(await historyCard.locator('.coordination-original').evaluate(node=>node.open),false);await historyCard.getByText('Original stored message',{exact:true}).click();assert.match(await historyCard.locator('.coordination-original pre').innerText(),/A bound peer sent this coordination message/);
  await historyCard.getByText('Original stored message',{exact:true}).click();await historyCard.getByRole('button',{name:'Inspect current task',exact:true}).click();await window.locator('#task-state').getByText('Completed',{exact:true}).waitFor();await window.locator('#task-close').click();
  await historyCard.scrollIntoViewIfNeeded();await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-coordination-history.png')});
  await window.locator(`#sessions button[data-session-id="${front.id}"]`).click();await window.locator('#agent-identity').filter({hasText:'frontend'}).waitFor();
  const spec = {name:'Users API',endpoint:'GET /users',schema_version:'1',schema:{type:'object',properties:{name:{type:'string'}}},workspace:backendWorkspace,owned_paths:['backend'],acceptance_criteria:['Frontend accepts the response'],artifacts:[{name:'Response schema',path:'backend/schema.json'}]};
  const created = await application.evaluate(async(_,{sessionID,spec})=>globalThis.workspaceFixtureRPC('coordination.contract',{session_id:sessionID,input:{action:'create',spec}}),{sessionID:back.id,spec});
  const contractID = created.result.id; assert.ok(contractID);
  await window.locator('#team-tab-contracts').click(); await window.getByText('Users API',{exact:true}).waitFor(); await window.locator('#workspace-contracts summary').first().click(); await window.getByText('Agreement needs review',{exact:true}).waitFor(); await window.getByText('backend/schema.json',{exact:true}).waitFor(); await window.getByText('Awaiting acknowledgement of revision 1 from frontend.',{exact:true}).waitFor(); assert.ok(!(await window.locator('#workspace-contracts').innerText()).includes('undefined'));
  await window.getByRole('button',{name:'Acknowledge revision 1',exact:true}).click();
  await window.getByText('Your acknowledgement is saved for this chat.',{exact:true}).waitFor();
  await window.getByText('Agreement current',{exact:true}).waitFor();
  assert.equal(await window.getByRole('button',{name:'Revision 1 acknowledged',exact:true}).isDisabled(),true);
  const changed = await application.evaluate(async(_,{sessionID,contractID,spec})=>globalThis.workspaceFixtureRPC('coordination.contract',{session_id:sessionID,input:{action:'update',contract_id:contractID,expected_revision:1,spec:{...spec,schema_version:'2',compatibility_changes:[{kind:'compatible',from_version:'1',to_version:'2',description:'Version label update; response schema retained.'}]}}}),{sessionID:back.id,contractID,spec});
  assert.equal(changed.result.revision,2);
  const staleRejected = await window.evaluate(async({sessionID,contractID})=>{try{await window.overkill.acknowledgeContract({sessionID,contractID,revision:1});return false;}catch{return true;}},{sessionID:front.id,contractID});
  assert.equal(staleRejected,true,'server rejects an acknowledgement for an old revision');
  await window.getByRole('button',{name:'Refresh agreements',exact:true}).click();
  await window.getByRole('button',{name:'Acknowledge revision 2',exact:true}).waitFor();
  await window.getByText('Agreement needs review',{exact:true}).waitFor();
  await window.getByRole('button',{name:'Acknowledge revision 2',exact:true}).click();
  await window.getByRole('button',{name:'Revision 2 acknowledged',exact:true}).waitFor();
  await window.getByText('Agreement current',{exact:true}).waitFor();
  await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-contract-inspector.png')});
  // Choose a named agreement and wait for the recipient's own acknowledgement.
  await window.locator('#team-tab-tasks').click();
  await window.locator('#peer-message').fill('Use the reviewed Users API agreement.');await window.locator('#peer-task-name').fill('agreement-gated');await window.locator('#peer-dependencies').fill('');
  await window.locator('#peer-choose-agreements').click();await window.locator('#request-agreements[open]').waitFor();
  await window.locator(`#request-agreement-list select[data-contract-id="${contractID}"]`).selectOption('2');
  await window.locator('#request-agreement-apply').click();assert.equal(await window.locator('#peer-contract-pins').inputValue(),`${contractID}@2`);
  const beforeAgreement=requestCalls;await window.locator('#peer-send').click();
  await window.locator('.peer-message').filter({hasText:'agreement-gated'}).getByRole('button',{name:'Inspect task',exact:true}).click();
  await window.locator('#task-dependencies').getByText('Waiting for prerequisites',{exact:true}).waitFor();
  assert.match(await window.locator('#task-dependencies').innerText(),/Awaiting acknowledgement of revision 2/);assert.equal(requestCalls,beforeAgreement,'unacknowledged agreement must prevent provider execution');
  await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-request-agreement-waiting.png')});await window.locator('#task-close').click();
  await window.locator(`#sessions button[data-session-id="${back.id}"]`).click(); await window.locator('#agent-identity').filter({hasText:'backend'}).waitFor();
  await window.locator('#team-tab-contracts').click();
  const recipientAgreement=window.locator('.contract-card').filter({hasText:'Users API'});await recipientAgreement.locator('summary').first().click();
  await recipientAgreement.getByRole('button',{name:'Acknowledge revision 2',exact:true}).click();
  await recipientAgreement.getByRole('button',{name:'Revision 2 acknowledged',exact:true}).waitFor();
  await window.locator(`#sessions button[data-session-id="${front.id}"]`).click(); await window.locator('#team-tab-tasks').click();await window.locator('#agent-identity').filter({hasText:'frontend'}).waitFor();
  await window.locator('.peer-message[data-state="completed"]').filter({hasText:'agreement-gated'}).first().waitFor({timeout:15000});assert.equal(requestCalls,beforeAgreement+1);
  await window.locator('#team-tab-contracts').click();
  // Native authoring uses the same selected-chat identity and owner checks.
  const backendCard=window.locator('.contract-card').filter({hasText:'Users API'});
  assert.equal(await backendCard.getByRole('button',{name:'Edit contract',exact:true}).count(),0);
  await window.locator('#contract-new').click();
  const editor=window.locator('#contract-editor');await editor.waitFor();
  await editor.locator('#contract-name').fill('Frontend client');await editor.locator('#contract-endpoint').fill('Users screen');
  await editor.locator('#contract-criteria').fill('Client accepts the users response');await editor.locator('#contract-paths').fill('frontend');
  await editor.locator('#contract-dependencies select').selectOption('2');
  await editor.locator('#contract-add-artifact').click();await editor.getByLabel('Artifact name',{exact:true}).fill('Client source');await editor.getByLabel('Relative path',{exact:true}).fill('frontend/client.js');
  await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-contract-editor.png')});
  await editor.locator('#contract-save').click();await editor.waitFor({state:'hidden'});
  const clientCard=window.locator('.contract-card').filter({hasText:'Frontend client'});await clientCard.waitFor();await clientCard.locator('summary').first().click();
  await clientCard.getByRole('button',{name:'Edit contract',exact:true}).click();await editor.waitFor();
  await editor.locator('#contract-version').fill('2');await editor.locator('#contract-change-kind').selectOption('compatible');await editor.locator('#contract-change').fill('Response shape retained.');
  await editor.locator('#contract-save').click();await editor.waitFor({state:'hidden'});
  await clientCard.getByText('Revision 2 · inspect agreement',{exact:true}).waitFor();
  const listed=await window.evaluate(sessionID=>window.overkill.contracts({sessionID}),front.id);
  const clientContract=listed.result.find(item=>item.spec.name==='Frontend client');assert.equal(clientContract.revision,2);assert.equal(clientContract.spec.dependencies[0].revision,2);assert.equal(clientContract.spec.artifacts[0].path,'frontend/client.js');
  // A stale open draft cannot overwrite a newer persisted revision.
  await clientCard.getByRole('button',{name:'Edit contract',exact:true}).click();
  await editor.locator('#contract-name').fill('Retain my stale draft');
  await application.evaluate(async(_,{sessionID,contract})=>globalThis.workspaceFixtureRPC('coordination.contract',{session_id:sessionID,input:{action:'update',contract_id:contract.id,expected_revision:contract.revision,spec:{...contract.spec,name:'Frontend latest'}}}),{sessionID:front.id,contract:clientContract});
  await editor.locator('#contract-save').click();await editor.locator('#contract-editor-status').filter({hasText:'Your draft is retained.'}).waitFor();
  assert.equal(await editor.locator('#contract-name').inputValue(),'Retain my stale draft');assert.equal(await editor.locator('#contract-save').isDisabled(),true);
  await editor.getByRole('button',{name:'Cancel',exact:true}).click();await editor.waitFor({state:'hidden'});

  await window.locator('#team-tab-checks').click(); await window.locator('#workspace-checks .check-card').first().waitFor(); assert.match(await window.locator('#workspace-checks').innerText(),/not.*task-completion verdict|not test evidence/); assert.match(await window.locator('#workspace-checks').innerText(),/Passed/);
  await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-workspace-checks.png')});
  // An explicit owner review raises the existing task budget and queues the
  // same blocked request again without discarding the first call's usage.
  await window.locator('#team-tab-tasks').click();await window.locator('#peer-message').fill('Replay after reviewing the first budget stop.');await window.locator('#peer-task-name').fill('reviewed-task');await window.locator('#peer-token-limit').fill('1');
  const callsBeforeReview=requestCalls;await window.locator('#peer-send').click();
  const reviewCard=window.locator('.peer-message').filter({hasText:'reviewed-task'}).filter({has:window.getByText('Needs review · request',{exact:true})});await reviewCard.waitFor({timeout:15000});
  await reviewCard.getByRole('button',{name:'Inspect task',exact:true}).click();await window.locator('#task-review').waitFor();const tokensBeforeReview=Number((await window.locator('#task-usage').innerText()).match(/(\d+) reported/)?.[1]);assert.ok(tokensBeforeReview>=42);await window.locator('#task-review').click();
  const reviewDialog=window.locator('#request-review');await reviewDialog.waitFor();await reviewDialog.locator('#request-review-tokens').fill('1000');await reviewDialog.locator('#request-review-note').fill('Reviewed the previous endpoint response; replay is safe.');
  await reviewDialog.locator('#request-review-confirm').check();await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-request-review-form.png')});await reviewDialog.locator('#request-review-save').click();
  await reviewDialog.waitFor({state:'hidden'});await window.locator('#task-close').click();
  await window.locator('.peer-message[data-state="completed"]').filter({hasText:'reviewed-task'}).getByText('Completed · request',{exact:true}).waitFor({timeout:15000});
  assert.equal(requestCalls,callsBeforeReview+2,'review replays once after the original budget-stopped call');
  await window.locator('.peer-message[data-state="completed"]').filter({hasText:'reviewed-task'}).filter({has:window.getByText('Completed · request',{exact:true})}).getByRole('button',{name:'Inspect task',exact:true}).click();
  await window.locator('#task-events').getByText('reviewed',{exact:true}).waitFor();const reviewedUsage=await window.locator('#task-usage').innerText();assert.match(reviewedUsage,/reported.*1,000 limit/);assert.ok(Number(reviewedUsage.match(/(\d+) reported/)?.[1])>=tokensBeforeReview+42,'confirmed usage from before replay must be preserved across peers');
  await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-reviewed-request.png')});await window.locator('#task-close').click();
  // A tiny real budget produces review evidence; the UI must never turn the
  // completed provider HTTP response into a successful task or free usage.
  await window.locator('#team-tab-tasks').click(); await window.locator('#peer-message').fill('Check another endpoint.'); await window.locator('#peer-task-name').fill('limited-task'); await window.locator('#peer-token-limit').fill('1'); await window.locator('#peer-send').click();
  await window.locator('.peer-message').filter({hasText:'limited-task'}).getByText('Needs review · request',{exact:true}).waitFor({timeout:15000});
  const limitedRequest=(await window.evaluate(sessionID=>window.overkill.correspondence({sessionID}),front.id)).messages.find(item=>item.task_id==='limited-task'&&item.kind==='request');assert.ok(limitedRequest);await window.locator(`#recorded-reviews .review-card[data-message-id="${limitedRequest.id}"]`).getByText('task token limit',{exact:true}).waitFor({timeout:15000});
  await window.locator('.peer-message[data-state="blocked"]').filter({hasText:'limited-task'}).getByRole('button',{name:'Review and resend',exact:true}).click();
  assert.equal(await window.locator('#peer-task-name').inputValue(),'limited-task'); assert.equal(await window.locator('#peer-token-limit').inputValue(),'1');
  // Delayed prefill must not overwrite a draft edited while it was loading.
  await application.evaluate(()=>{globalThis.holdWorkspaceUsage=true;});
  await window.locator('.peer-message[data-state="blocked"]').filter({hasText:'limited-task'}).getByRole('button',{name:'Review and resend',exact:true}).click();
  await window.locator('#peer-message').fill('Keep my edited draft.');
  await application.evaluate(()=>{globalThis.holdWorkspaceUsage=false;globalThis.releaseWorkspaceUsage();});
  await window.locator('.peer-message[data-state="blocked"]').filter({hasText:'limited-task'}).getByRole('button',{name:'Review and resend',exact:true}).waitFor();
  assert.equal(await window.locator('#peer-message').inputValue(),'Keep my edited draft.');
  await window.locator('#peer-reuse-clear').click();
  await window.locator('.peer-message[data-state="blocked"]').filter({hasText:'limited-task'}).getByRole('button',{name:'Cancel request',exact:true}).click();
  await window.locator('.peer-message[data-state="cancelled"]').filter({hasText:'limited-task'}).waitFor();
  await window.locator('#peer-message').fill('Unnamed original request.'); await window.locator('#peer-task-name').fill(''); await window.locator('#peer-token-limit').fill('1'); await window.locator('#peer-send').click();
  const unnamed = window.locator('.peer-message[data-state="blocked"]').filter({hasText:'Unnamed original request.'}); await unnamed.waitFor({timeout:15000});
  await unnamed.getByRole('button',{name:'Review and resend',exact:true}).click(); await window.locator('#peer-reuse-note').waitFor();
  await unnamed.getByRole('button',{name:'Cancel request',exact:true}).click();
  await window.locator('.peer-message[data-state="cancelled"]').filter({hasText:'Unnamed original request.'}).waitFor();
  const beforeRetry=requestCalls; await window.locator('#peer-send').click();
  await window.waitForFunction(()=>Array.from(document.querySelectorAll('.peer-message')).filter(node=>node.textContent.includes('Unnamed original request.')).length===2 && Array.from(document.querySelectorAll('.peer-message[data-state="blocked"]')).some(node=>node.textContent.includes('Unnamed original request.')));
  assert.equal(requestCalls,beforeRetry,'unnamed resend must keep the exhausted conversation scope');
  await window.locator('.peer-message[data-state="blocked"]').filter({hasText:'Unnamed original request.'}).getByRole('button',{name:'Cancel request',exact:true}).click();
  // A cost limit configured outside this narrow composer must also survive.
  await application.evaluate(async(_,sessionID)=>globalThis.workspaceFixtureRPC('coordination.send',{session_id:sessionID,to:'backend',kind:'request',task_id:'priced-task',payload:'Priced original request.',idempotency_key:'priced-initial',usage_budget:{tokens:100,cost_usd:.5}}),front.id);
  await window.locator('.peer-message[data-state="blocked"]').filter({hasText:'priced-task'}).waitFor({timeout:15000});
  await window.locator('.peer-message[data-state="blocked"]').filter({hasText:'priced-task'}).getByRole('button',{name:'Review and resend',exact:true}).click(); await window.locator('#peer-reuse-note').filter({hasText:'Cost limit: $0.5'}).waitFor();
  await window.locator('.peer-message[data-state="blocked"]').filter({hasText:'priced-task'}).getByRole('button',{name:'Cancel request',exact:true}).click();
  const beforePriced=requestCalls; await window.locator('#peer-send').click();
  await window.waitForFunction(()=>Array.from(document.querySelectorAll('.peer-message')).filter(node=>node.textContent.includes('priced-task')).length===2 && Array.from(document.querySelectorAll('.peer-message[data-state="blocked"]')).some(node=>node.textContent.includes('priced-task')));
  assert.equal(requestCalls,beforePriced,'cost budget resend must neither reset nor conflict');
  assert.match(await window.locator('#peer-reuse-note').innerText(),/Cost limit: \$0.5/);
  assert.equal(await window.locator('#peer-token-limit').isDisabled(),true,'remaining task budget stays bound until explicitly cleared');
    await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-workspace-overview.png')});
  await window.setViewportSize({width:850,height:800}); await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-workspace-narrow.png')});
  assert.equal(await window.locator('#agents-toggle').isVisible(),true);
  const toggleBox=await window.locator('#agents-toggle').boundingBox(),refreshBox=await window.locator('#refresh').boundingBox();
  assert.ok(toggleBox&&refreshBox);assert.ok(refreshBox.x+refreshBox.width<=toggleBox.x || toggleBox.x+toggleBox.width<=refreshBox.x || refreshBox.y+refreshBox.height<=toggleBox.y || toggleBox.y+toggleBox.height<=refreshBox.y,'narrow header controls must not overlap');

  // Explicit review preference runs the actual shared backend loop. Its final
  // non-streamed revision must replace the first streamed answer in the UI.
  await window.locator('#settings-button').click();await window.locator('#settings[open]').waitFor();
  assert.equal(await window.locator('#workspace-instructions').isChecked(),true);
  await window.locator('#autonomous-mode').check();await window.locator('#settings-save').click();await settingsConnected();
  if(await window.locator('#agents-panel').isVisible())await window.locator('#agents-toggle').click();
  await application.evaluate(({dialog},folder)=>{dialog.showOpenDialog=async()=>({canceled:false,filePaths:[folder]});},workspace);await window.locator('#workspace').click();
  await window.locator('#new-session').click();await window.locator('#message').fill('Explain the review fixture.');await window.locator('#send').click();
  await window.getByText('Review fixture revised.',{exact:true}).waitFor({timeout:30000});await window.locator('#stop').waitFor({state:'hidden'});
  assert.equal(await window.getByText('Review fixture draft.',{exact:true}).count(),0);
  const reviewSession=(await window.evaluate(()=>window.overkill.listSessions())).sessions.find(item=>item.id!==front.id && item.id!==back.id);
  assert.ok(reviewSession);
  const observed=await window.evaluate(sessionID=>window.overkill.evaluation({sessionID}),reviewSession.id);
  assert.equal(observed.iterative_evaluation.status,'accepted');assert.equal(observed.iterative_evaluation.iteration,2);assert.equal(observed.red_team_passed,null);
  await window.locator('#message').fill(`/build ${planFile}`);await window.locator('#send').click();
  await window.getByText('Native phase 2 complete.',{exact:true}).waitFor({timeout:30000});await window.locator('#stop').waitFor({state:'hidden'});
  const planStatus=await window.evaluate(sessionID=>window.overkill.evaluation({sessionID}),reviewSession.id);
  assert.equal(planStatus.plan_execution.status,'completed');assert.equal(planStatus.plan_execution.current,true);assert.deepEqual(planStatus.plan_execution.phases.map(phase=>phase.status),['done','done']);
  await window.locator('#agents-toggle').click();await window.locator('#team-tab-checks').click();await window.getByText('Loaded plan: Native loaded plan',{exact:true}).waitFor();
  await window.screenshot({path:path.resolve(__dirname,'../../.cache/desktop-loaded-plan.png')});
  const rejected = await window.evaluate(async()=>{try{await window.overkill.contractReadiness({sessionID:'not-a-session',contractID:'x',action:'delete'});return false;}catch{return true;}});assert.equal(rejected,true);
});
