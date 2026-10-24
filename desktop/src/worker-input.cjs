'use strict';
function validateWorkers(input) {
  const fields={list:[],inbox:['after','waitMS','recent'],send:['to','kind','content','idempotencyKey','inReplyTo'],acknowledge:['messageID']};
  if(!input||typeof input!=='object'||Array.isArray(input)||!Object.hasOwn(fields,input.operation))throw new Error('Unknown worker operation.');
  if(Object.keys(input).some(key=>!['operation','sessionID',...fields[input.operation]].includes(key)))throw new Error('Unexpected worker field.');
  if(typeof input.sessionID!=='string'||!/^[\w-]{1,128}$/.test(input.sessionID))throw new Error('Choose a chat first.');
  const params={session_id:input.sessionID};
  if(input.operation==='list')return {method:'workers.list',params};
  const value={action:input.operation};
  if(input.operation==='send'){
    for(const [key,max]of [['to',128],['content',16000],['idempotencyKey',128]])if(typeof input[key]!=='string'||!input[key]||Buffer.byteLength(input[key],'utf8')>max)throw new Error('Worker message is missing or too large.');
    if(!['update','question','answer','steer'].includes(input.kind))throw new Error('Choose a message intent.');
    Object.assign(value,{to:input.to,kind:input.kind,content:input.content,idempotency_key:input.idempotencyKey});
    if(input.inReplyTo!==undefined){if(typeof input.inReplyTo!=='string'||input.inReplyTo.length>128)throw new Error('Invalid reply identity.');value.in_reply_to=input.inReplyTo;}
  }
  if(input.operation==='inbox')for(const [key,max]of [['after',Number.MAX_SAFE_INTEGER],['waitMS',1000]])if(input[key]!==undefined){if(!Number.isSafeInteger(input[key])||input[key]<0||input[key]>max)throw new Error('Invalid worker inbox bounds.');value[key==='waitMS'?'wait_ms':key]=input[key];}
  if(input.operation==='acknowledge'){if(typeof input.messageID!=='string'||input.messageID.length>128||!input.messageID)throw new Error('Choose a message.');value.message_id=input.messageID;}
  if(input.recent!==undefined){if(typeof input.recent!=='boolean')throw new Error('Invalid inbox snapshot.');value.recent=input.recent;}
  params.input=value;return {method:'workers.'+input.operation,params};
}
module.exports={validateWorkers};
