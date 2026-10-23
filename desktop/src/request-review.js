(function(root){
 'use strict';
 function create({document:doc,api,session,onSaved,reviewID=()=>globalThis.crypto.randomUUID()}){
  const el=(tag,text,id)=>{const node=doc.createElement(tag);if(text!==undefined)node.textContent=text;if(id)node.id=id;return node;};
  const dialog=el('dialog',undefined,'request-review'),form=el('form');dialog.setAttribute('aria-labelledby','request-review-title');
  const title=el('h2','Review budget and replay','request-review-title'),summary=el('p',undefined,'request-review-summary');
  const tokens=el('input',undefined,'request-review-tokens'),cost=el('input',undefined,'request-review-cost'),note=el('textarea',undefined,'request-review-note'),confirmation=el('input',undefined,'request-review-confirm');
  tokens.type=cost.type='number';tokens.min=cost.min='0';tokens.step='1';cost.step='any';tokens.max='1000000000000';cost.max='1000000';note.maxLength=2048;confirmation.type='checkbox';
  const label=(text,input)=>{const row=el('label',text);row.append(input);return row;};
  const status=el('p',undefined,'request-review-status');status.setAttribute('role','status');
  const save=el('button','Save budget and requeue','request-review-save'),cancel=el('button','Cancel','request-review-cancel');save.type='submit';cancel.type='button';
  form.append(title,summary,el('p','This queues another execution of the original request. Earlier file writes or external effects may happen again. Dependencies, agreement pins, deadlines, attempts and usage evidence stay in force.','request-review-warning'),label('Task token limit (0 means no limit)',tokens),label('Estimated cost limit in USD (0 means no limit)',cost),label('Review note',note),label('I reviewed prior effects and authorize replay of this request.',confirmation),status,cancel,save);dialog.append(form);doc.body.append(dialog);
  let current=null;
  const valid=state=>current===state&&session()?.id===state.sessionID;
  function controls(state){for(const field of [tokens,cost,note,confirmation])field.disabled=state.pending||!!state.input;save.disabled=state.pending;cancel.disabled=state.pending;}
  function reset(){current=null;if(dialog.open)dialog.close();}
  function open(message,usage){
   reset();const sid=session()?.id;if(!sid||message?.state!=='blocked'||message.kind!=='request'||!Number.isSafeInteger(usage?.revision)||usage.revision<1)throw new Error('Inspect a blocked request and its current budget first.');
   const state={sessionID:sid,message:structuredClone(message),usage:structuredClone(usage),pending:false,input:null,reviewID:reviewID()};current=state;
   tokens.value=String(usage.limits?.tokens??0);cost.value=String(usage.limits?.cost_usd??0);note.value='';confirmation.checked=false;status.textContent='';save.textContent='Save budget and requeue';
   const costText=usage.unknown_usage||usage.unknown_price||usage.pending_executions||usage.unconfirmed_executions||!Number.isFinite(usage.estimated_cost_usd)?'Cost estimate incomplete.':`Estimated cost: $${usage.estimated_cost_usd}.`;
   summary.textContent=`${message.task_id||'Peer request'} · request revision ${message.revision} · budget revision ${usage.revision} · ${(usage.input_tokens||0)+(usage.output_tokens||0)} reported tokens. ${costText} Limits apply to all agents and requests in ${usage.scope||message.task_id||'this budget scope'}.`;
   controls(state);dialog.showModal();note.focus();
  }
  form.addEventListener('submit',async event=>{
   event.preventDefault();const state=current;if(!state||!valid(state)||state.pending)return;
   if(!state.input){
    const tokenLimit=Number(tokens.value),costLimit=Number(cost.value),reason=note.value.trim();
    if(!tokens.value.trim()||!cost.value.trim()||!Number.isSafeInteger(tokenLimit)||tokenLimit<0||tokenLimit>1e12||!Number.isFinite(costLimit)||costLimit<0||costLimit>1e6||!reason||new TextEncoder().encode(reason).length>2048||!confirmation.checked){status.textContent='Enter valid explicit limits and a review note, then confirm replay.';return;}
    state.input={sessionID:state.sessionID,messageID:state.message.id,revision:state.message.revision,usageRevision:state.usage.revision,reviewID:state.reviewID,note:reason,tokenLimit,costLimit,requeue:true};
   }
   state.pending=true;controls(state);status.textContent='Recording review…';
   try{
    const result=await api.reviewRequest(structuredClone(state.input));if(!valid(state))return;
    reset();await onSaved?.(result);
   }catch(error){if(valid(state)){status.textContent=`${error.message} The review may have committed. Retry submits the same review only; close and refresh to inspect current state before changing it.`;save.textContent='Retry same review';}}
   finally{if(valid(state)){state.pending=false;controls(state);}}
  });
  cancel.addEventListener('click',reset);dialog.addEventListener('close',()=>{current=null;});dialog.addEventListener('cancel',event=>{if(current?.pending)event.preventDefault();else reset();});
  return{open,reset};
 }
 const exported={create};if(typeof module==='object'&&module.exports)module.exports=exported;else root.overkillRequestReview=exported;
})(typeof window==='undefined'?globalThis:window);
