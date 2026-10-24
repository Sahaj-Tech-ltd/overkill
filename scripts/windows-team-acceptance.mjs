// A controlled provider drives actual installed-app file, process and HTTP
// effects. Vendor responses are fixtures; the coding and terminal work is real.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {randomUUID} from 'node:crypto';

const quote=value=>"'"+value.replaceAll("'","''")+"'";
const tool=(name,input)=>({tool_calls:[{id:randomUUID(),type:'function',function:{name,arguments:JSON.stringify(input)}}]});
const serverSource="const http=require('node:http'),fs=require('node:fs'),readline=require('node:readline');const server=http.createServer((req,res)=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify({id:'windows-u1'}));});server.listen(0,'127.0.0.1',()=>{fs.writeFileSync('server-port',String(server.address().port));console.log('WINDOWS_SERVER_READY');});readline.createInterface({input:process.stdin}).on('line',line=>console.log('WINDOWS_TYPED:'+line));\n";
const checkSource="const http=require('node:http'),fs=require('node:fs'),assert=require('node:assert/strict');http.get('http://127.0.0.1:'+fs.readFileSync('server-port','utf8'),res=>{let text='';res.on('data',chunk=>text+=chunk);res.on('end',()=>{assert.equal(JSON.parse(text).id,'windows-u1');fs.writeFileSync('integration-result','WINDOWS_INTEGRATION_PASSED');console.log('WINDOWS_INTEGRATION_PASSED');});}).on('error',error=>{throw error;});\n";

export class WindowsTeamAcceptance {
  constructor(workspace,nodePath){Object.assign(this,{workspace,nodePath,parentCalls:0,backendCalls:0,frontendCalls:0,backendStage:'write',frontendStage:'write'});this.answerReady=new Promise(resolve=>{this.answerPublished=resolve;});}
  matches(body){return body.messages.some(m=>m.role==='user'&&/WINDOWS_TEAM_ACCEPTANCE|WINDOWS_BACKEND_CHILD|WINDOWS_FRONTEND_CHILD/.test(m.content||''));}
  command(file){return '& '+quote(this.nodePath)+' '+quote(file);}
  async respond(body){
    const goal=body.messages.find(m=>m.role==='user'&&m.content?.startsWith('Delegated task:\n'))?.content||'';
    const last=()=>JSON.parse(body.messages.filter(m=>m.role==='tool').at(-1).content);
    if(goal.includes('WINDOWS_BACKEND_CHILD')){
      this.backendCalls++;
      if(this.backendStage==='write'){this.backendStage='start';return tool('fs',{action:'write',path:'server.cjs',content:serverSource});}
      if(this.backendStage==='start'){this.backendStage='ready';return tool('terminal',{operation:'start',command:this.command('server.cjs')});}
      if(this.backendStage==='ready'){
        const result=last();this.serverID ||= result.id;this.revision ||= result.control_revision;
        if(!result.output?.includes('WINDOWS_SERVER_READY'))return tool('terminal',{operation:'read',id:this.serverID,wait_ms:1000});
        this.backendStage='question';return tool('worker_messages',{action:'inbox',wait_ms:1000});
      }
      if(this.backendStage==='question'){
        const question=last().messages.find(m=>m.kind==='question');
        if(!question)return tool('worker_messages',{action:'inbox',wait_ms:1000});
        this.backendStage='handoff';return tool('worker_messages',{action:'send',to:'frontend',kind:'answer',content:'Read server-port and GET /; response has id:string.',in_reply_to:question.id,idempotency_key:'windows-schema-answer'});
      }
      if(this.backendStage==='handoff'){assert.equal(last().kind,'answer');this.answerPublished();this.backendStage='finish';return tool('terminal',{operation:'handoff',id:this.serverID,control_revision:this.revision});}
      this.handoff=last();assert.equal(this.handoff.success,true);assert.equal(this.handoff.handoff.state,'applied');assert.equal(this.handoff.terminal.owner.worker,'main');
      return {content:'Backend server handed to Main chat after answering frontend.'};
    }
    if(goal.includes('WINDOWS_FRONTEND_CHILD')){
      this.frontendCalls++;
      if(this.frontendStage==='write'){this.frontendStage='question';return tool('fs',{action:'write',path:'frontend-check.cjs',content:checkSource});}
      if(this.frontendStage==='question'){this.frontendStage='answer';return tool('worker_messages',{action:'send',to:'backend',kind:'question',content:'Where is the endpoint and what does it return?',idempotency_key:'windows-schema-question'});}
      if(this.frontendStage==='answer'){
        if(!last().messages?.some(m=>m.kind==='answer')){await this.answerReady;return tool('worker_messages',{action:'inbox',wait_ms:1000});}
        this.frontendStage='check';return tool('pty_shell',{command:this.command('frontend-check.cjs'),timeout_seconds:15});
      }
      if(this.frontendStage==='check'){
        this.check=last();assert.equal(this.check.exit_code,0);assert.match(this.check.output,/WINDOWS_INTEGRATION_PASSED/);
        this.frontendStage='finish';return tool('worker_messages',{action:'send',to:'parent',kind:'update',content:'Actual Windows frontend/backend integration passed.',idempotency_key:'windows-team-result'});
      }
      return {content:'Frontend received backend schema and passed the real HTTP integration check.'};
    }
    this.parentCalls++;
    if(this.parentCalls===1)return tool('delegate_task',{tasks:[{name:'backend',goal:'WINDOWS_BACKEND_CHILD: write and run the server; answer frontend with its schema, then transfer the terminal to Main chat.'},{name:'frontend',goal:'WINDOWS_FRONTEND_CHILD: prepare the check, ask backend for its schema, and run the actual HTTP integration using pty_shell.'}]});
    if(this.parentCalls===2){this.report=last();assert.equal(this.report.results.length,2);assert.ok(this.report.results.every(r=>r.status==='completed'),JSON.stringify(this.report));return tool('worker_messages',{action:'inbox'});}
    if(this.parentCalls===3){assert.ok(last().messages.some(m=>m.content==='Actual Windows frontend/backend integration passed.'));return tool('terminal',{operation:'read',id:this.serverID,wait_ms:200});}
    assert.equal(last().terminal.owner.worker,'main');assert.equal(last().terminal.state,'running');
    return {content:'Windows team integration and terminal handoff passed.'};
  }
  async exercise(page,session,output){
    await page.locator('#message').fill('WINDOWS_TEAM_ACCEPTANCE: build frontend/backend workers, exchange the schema, and keep the server in Main chat.');await page.locator('#send').click();
    await page.getByText('Windows team integration and terminal handoff passed.',{exact:true}).waitFor({timeout:60000});await page.locator('#stop').waitFor({state:'hidden'});
    assert.equal(await fs.readFile(path.join(this.workspace,'integration-result'),'utf8'),'WINDOWS_INTEGRATION_PASSED');
    const port=Number(await fs.readFile(path.join(this.workspace,'server-port'),'utf8'));assert.deepEqual(await (await fetch('http://127.0.0.1:'+port)).json(),{id:'windows-u1'});
    const inventory=await page.evaluate(id=>window.overkill.workers({operation:'list',sessionID:id}),session.id);assert.equal(inventory.workers.length,2);assert.ok(inventory.workers.every(w=>w.status==='completed'));assert.equal(inventory.terminal_handoffs[0].receipt.id,this.handoff.handoff.id);
    await page.locator('#terminal-toggle').click();await page.waitForFunction(()=>!document.getElementById('terminal-take-control').disabled);await page.locator('#terminal-take-control').click();await page.locator('#live-terminal-status').filter({hasText:'You have control'}).waitFor();
    const frame=page.frameLocator('#live-terminal-frame');assert.equal(await frame.locator('body').evaluate(()=>typeof window.overkill),'undefined');
    await frame.locator('.xterm-helper-textarea').pressSequentially('FROM_INSTALLED_UI');await frame.locator('.xterm-helper-textarea').press('Enter');
    let observed;
    for(let i=0;i<100;i++){observed=await page.evaluate(({sessionID,id})=>window.overkill.terminal({operation:'read',sessionID,id,waitMS:100}),{sessionID:session.id,id:this.serverID});if(observed.output.includes('WINDOWS_TYPED:FROM_INSTALLED_UI'))break;await new Promise(resolve=>setTimeout(resolve,30));}
    assert.match(observed.output,/WINDOWS_TYPED:FROM_INSTALLED_UI/);
    await page.locator('#terminal-return-control').click();await page.locator('#live-terminal-status').filter({hasText:'Agent has control'}).waitFor();await page.screenshot({path:path.join(output,'windows-installed-team-terminal.png')});
    await page.locator('#terminal-end').click();await page.locator('#live-terminal-status').filter({hasText:'closed'}).waitFor();await assert.rejects(fetch('http://127.0.0.1:'+port));
    const receipt={parentCalls:this.parentCalls,backendCalls:this.backendCalls,frontendCalls:this.frontendCalls,report:this.report,check:this.check,handoff:this.handoff,inventory,observed,scope:this.scope||'Actual installed Windows app under an ordinary account, real fs edits, sibling question/answer, real HTTP integration in ConPTY, child server handoff, emulator keyboard takeover/return and job cleanup. Controlled local provider; no paid account.'};
    await fs.writeFile(path.join(output,'windows-installed-team-acceptance.json'),JSON.stringify(receipt,null,2)+'\n');return receipt;
  }
}
