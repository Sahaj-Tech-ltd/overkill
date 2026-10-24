'use strict';
window.overkillWorkers = (() => {
  const $ = id => document.getElementById(id);
  let sessionID=null,connected=false,generation=0,polling=false,sending=false,workers=[],draft=null,replyID=null;
  const readable=error=>String(error.message||error).replace(/^Error invoking remote method '[^']+': (?:Error: )?/,'');
  function clearReply(){replyID=null;$('worker-reply-note').textContent='';$('worker-reply-cancel').hidden=true;if($('worker-kind').value==='answer')$('worker-kind').value='update';}
  $('worker-reply-cancel').addEventListener('click',clearReply);$('worker-recipient').addEventListener('change',clearReply);
  const names=()=>new Map(workers.map(worker=>[worker.id,worker.name]));
  function controls(){
    $('worker-send').disabled=!connected||!sessionID||sending||!workers.some(w=>w.live&&w.status==='running');
    $('worker-recipient').disabled=$('worker-send').disabled;
  }
  function render(items){
    workers=items;const selected=$('worker-recipient').value,labels=names();
    $('task-workers').replaceChildren();$('worker-recipient').replaceChildren();$('sidebar-workers').replaceChildren();
    for(const w of items){
      const row=document.createElement('article');row.className='worker-card';row.dataset.workerId=w.id;
      const title=document.createElement('strong');title.textContent=w.name;
      const state=document.createElement('span');state.className='muted';
      state.textContent=(!w.live?'Previous runtime · ':'')+(w.status==='running'&&!w.live?'outcome unconfirmed':w.status)+(w.model?' · '+w.model:'');
      const goal=document.createElement('p');goal.textContent=w.goal;
      row.append(title,state,goal);
      if(w.followup_of){const previous=document.createElement('p');previous.className='muted';previous.textContent='Follow-up to '+(labels.get(w.followup_of)||w.followup_of);row.append(previous);}
      if(w.result){const details=document.createElement('details'),summary=document.createElement('summary'),body=document.createElement('pre');summary.textContent='Reported result · review checks separately';body.textContent=w.result.summary||w.result.error||w.result.status;details.append(summary,body);row.append(details);}
      $('task-workers').append(row);
      if(w.live&&w.status==='running'){
        const option=document.createElement('option');option.value=w.id;option.textContent=w.name;$('worker-recipient').append(option);
        const button=document.createElement('button');button.type='button';button.className='sidebar-agent';
        const name=document.createElement('strong');name.textContent=w.name;const activity=document.createElement('span');activity.textContent='Child worker · running';button.append(name,activity);
        button.addEventListener('click',()=>{window.overkillShell.open('team');$('worker-recipient').value=w.id;$('worker-content').focus();});$('sidebar-workers').append(button);
      }
    }
    if([...$('worker-recipient').options].some(o=>o.value===selected))$('worker-recipient').value=selected;
    controls();
  }
  function handoffs(items){
    $('worker-terminal-handoffs').replaceChildren();const labels=names();
    for(const item of items.slice(0,20)){const row=document.createElement('p');row.className='muted';row.textContent=(item.current_runtime?'':'Previous runtime · ')+(labels.get(item.receipt.from.worker)||item.receipt.from.worker)+' → Main chat · '+(item.receipt.state==='applied'?'terminal ownership transferred':'handoff needs confirmation')+' · '+item.receipt.terminal_id;$('worker-terminal-handoffs').append(row);}
  }
  function correspondence(messages){
    const labels=names();labels.set('parent','Main chat / you');$('worker-correspondence').replaceChildren();
    for(const item of messages.slice(-20)){
      const row=document.createElement('article');row.className='worker-correspondence';row.dataset.messageId=item.id;
      const label=document.createElement('strong');label.textContent=`${labels.get(item.from)||item.from} → ${labels.get(item.to)||item.to} · ${item.kind}`;
      const state=document.createElement('span');state.className='muted';state.textContent={accepted:'Queued',admitted:'Added to context',acknowledged:'Acknowledged'}[item.state]||item.state;
      const text=document.createElement('p');text.textContent=item.content;row.append(label,state,text);
      if(item.kind==='question'&&item.to==='parent'&&workers.some(w=>w.id===item.from&&w.live&&w.status==='running')){
        const reply=document.createElement('button');reply.type='button';reply.textContent='Reply';reply.addEventListener('click',()=>{$('worker-recipient').value=item.from;$('worker-kind').value='answer';replyID=item.id;$('worker-reply-note').textContent='Replying to '+(labels.get(item.from)||item.from)+': '+item.content;$('worker-reply-cancel').hidden=false;$('worker-content').focus();});row.append(reply);
      }
      $('worker-correspondence').append(row);
    }
  }
  async function poll(){
    if(polling||!sessionID||!connected||document.hidden)return;
    const epoch=generation,sid=sessionID;polling=true;
    try{
      const result=await window.overkill.workers({operation:'list',sessionID:sid});if(epoch!==generation)return;
      render(result.workers||[]);handoffs(result.terminal_handoffs||[]);
      if(!result.available){$('worker-status').textContent=result.reason;correspondence([]);return;}
      const inbox=await window.overkill.workers({operation:'inbox',sessionID:sid,recent:true});if(epoch!==generation)return;
      correspondence(inbox.messages||[]);
      $('worker-status').textContent=workers.length?'Worker results are reports; check the actual files and tests before accepting.':'Named child workers appear when this chat delegates work.';
    }catch(error){if(epoch===generation)$('worker-status').textContent=readable(error);}
    finally{polling=false;}
  }
  $('worker-form').addEventListener('submit',async event=>{
    event.preventDefault();if($('worker-send').disabled)return;
    const epoch=generation,body={operation:'send',sessionID,to:$('worker-recipient').value,kind:$('worker-kind').value,content:$('worker-content').value};
    if(replyID)body.inReplyTo=replyID;
    const fingerprint=JSON.stringify(body);
    if(draft?.fingerprint!==fingerprint)draft={fingerprint,key:crypto.randomUUID()};
    sending=true;$('worker-send-status').textContent='Saving message…';controls();
    try{
      const result=await window.overkill.workers({...body,idempotencyKey:draft.key});
      if(epoch!==generation)return;
      if($('worker-content').value===body.content)$('worker-content').value='';draft=null;clearReply();
      $('worker-send-status').textContent=`${result.state==='accepted'?'Queued':'Already delivered'} · ${result.id}. This does not start a new task.`;
      void poll();
    }catch(error){if(epoch===generation)$('worker-send-status').textContent=readable(error)+' Retry keeps the same message identity while this draft is unchanged.';}
    finally{sending=false;controls();}
  });
  setInterval(()=>void poll(),1200);controls();
  return {
    label(id){return workers.find(w=>w.id===id)?.name;},
    select(value){sessionID=value?.id||null;generation++;draft=null;clearReply();render([]);handoffs([]);correspondence([]);$('worker-send-status').textContent='';$('worker-content').value='';void poll();},
    connection(value){if(connected===value)return;connected=value;generation++;controls();if(!value)$('worker-status').textContent='Disconnected. Last reported worker state may be stale.';else void poll();}
  };
})();
