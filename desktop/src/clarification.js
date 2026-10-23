(function(root){
 'use strict';
 function create({document:doc,api,getSession,isReady,onAnswered}){
  const el=(tag,text,id)=>{const node=doc.createElement(tag);if(text!==undefined)node.textContent=text;if(id)node.id=id;return node;};
  const dialog=el('dialog',undefined,'clarification'),form=el('form'),question=el('pre',undefined,'clarification-question'),choices=el('select',undefined,'clarification-choice'),text=el('textarea',undefined,'clarification-text'),status=el('p',undefined,'clarification-status'),send=el('button','Send answer','clarification-send'),cancel=el('button','Cancel clarification','clarification-cancel');dialog.setAttribute('aria-labelledby','clarification-title');text.maxLength=8192;status.setAttribute('role','status');send.type='submit';cancel.type='button';
  const label=(title,input)=>{const node=el('label',title);node.append(input);return node;};form.append(el('h2','Clarification needed','clarification-title'),el('p','Answer the pending question for this chat. Cancelling declines this question; it does not undo earlier effects.'),question,label('Response',choices),label('Your answer',text),status,cancel,send);dialog.append(form);doc.body.append(dialog);
  const drafts=new Map(),answered=new Set();let active=null,epoch=0,polling=null;
  const valid=(state,generation=epoch)=>active===state&&generation===epoch&&getSession()?.id===state.sessionID&&isReady();
  function controls(state){const busy=state?.pending===true;send.disabled=busy;cancel.disabled=busy;choices.disabled=busy;text.disabled=busy||choices.value!=='-1';}
  function reset(){epoch++;active=null;if(dialog.open)dialog.close();}
  function show(state){active=state;question.textContent=state.question;choices.replaceChildren();const free=el('option','Write an answer');free.value='-1';choices.append(free);state.choices.forEach((choice,index)=>{const option=el('option',choice);option.value=String(index);choices.append(option);});choices.value=String(state.index);text.value=state.text;status.textContent=state.error||'';controls(state);if(!dialog.open)dialog.showModal();}
  async function sync(){const sessionID=getSession()?.id;if(!sessionID||!isReady()){if(active)reset();return;}const generation=epoch,key=sessionID+':'+generation;if(polling===key)return;polling=key;
   try{const result=await api.clarifyPoll(sessionID);if(generation!==epoch||getSession()?.id!==sessionID||!isReady())return;if(!result){if(active){active=null;if(dialog.open)dialog.close();}return;}if(result.choices===null)result.choices=[];if(result.session_id!==sessionID||typeof result.question_id!=='string'||!result.question_id||typeof result.question!=='string'||!Array.isArray(result.choices)||result.choices.length>32||result.choices.some(value=>typeof value!=='string'))throw new Error('Invalid clarification response.');const identity=sessionID+':'+result.question_id;if(answered.has(identity))return;if(active?.key===identity)return;
    let state=drafts.get(identity);if(!state){state={key:identity,sessionID,questionID:result.question_id,question:result.question,choices:[...result.choices],index:-1,text:'',pending:false,error:''};drafts.set(identity,state);if(drafts.size>32)drafts.delete(drafts.keys().next().value);}show(state);
   }catch(error){if(generation===epoch&&getSession()?.id===sessionID&&active){active.error=error.message;status.textContent=error.message;}}
   finally{if(polling===key)polling=null;}
  }
  choices.addEventListener('change',()=>{if(active){active.index=Number(choices.value);controls(active);}});text.addEventListener('input',()=>{if(active)active.text=text.value;});
  async function reply(decline){const state=active,generation=epoch;if(!state||!valid(state)||state.pending)return;const index=decline?-1:state.index;const answer=decline?'':index>=0?state.choices[index]:state.text;if(!decline&&!answer?.trim()){status.textContent='Choose a response or enter your answer.';return;}state.pending=true;state.error='';status.textContent=decline?'Cancelling clarification…':'Sending answer…';controls(state);
   try{await api.answerQuestion({sessionID:state.sessionID,questionID:state.questionID,text:answer,index,cancel:decline});if(!valid(state,generation))return;answered.add(state.key);if(answered.size>128)answered.delete(answered.values().next().value);drafts.delete(state.key);active=null;dialog.close();onAnswered?.(decline);}
   catch(error){if(valid(state,generation)){state.error=error.message;status.textContent=error.message;}}
   finally{state.pending=false;if(valid(state))controls(state);}
  }
  form.addEventListener('submit',event=>{event.preventDefault();return reply(false);});cancel.addEventListener('click',()=>reply(true));dialog.addEventListener('cancel',event=>{event.preventDefault();void reply(true);});
  return{sync,reset};
 }
 const exported={create};if(typeof module==='object'&&module.exports)module.exports=exported;else root.overkillClarification=exported;
})(typeof globalThis!=='undefined'?globalThis:this);
