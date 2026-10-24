'use strict';
const fields={list:[],read:['id','cursor','limit','waitMS'],input:['id','data','bytes','controlRevision','sequence'],resize:['id','rows','cols','controlRevision'],control:['id','release','controlRevision'],interrupt:['id','controlRevision','sequence'],close:['id','controlRevision'],handoff:['id','controlRevision'],forget:['id']};
function validateTerminal(input){
 if(!input||typeof input!=='object'||Array.isArray(input)||!Object.hasOwn(fields,input.operation))throw new Error('Unknown terminal operation.');
 const allowed=new Set(['operation','sessionID',...fields[input.operation]]);
 if(Object.keys(input).some(key=>!allowed.has(key)))throw new Error('Unexpected terminal field.');
 if(typeof input.sessionID!=='string'||!/^[\w-]{1,128}$/.test(input.sessionID))throw new Error('Choose a chat first.');
 const params={session_id:input.sessionID};
 if(input.operation!=='list'){if(typeof input.id!=='string'||!/^[a-f\d]{8}-[a-f\d-]{27}$/.test(input.id))throw new Error('Choose a valid terminal.');params.id=input.id;}
 for(const [key,max]of Object.entries({cursor:Number.MAX_SAFE_INTEGER,limit:65536,waitMS:1000,rows:1000,cols:1000}))if(input[key]!==undefined){if(!Number.isSafeInteger(input[key])||input[key]<0||input[key]>max||(['rows','cols'].includes(key)&&input[key]===0))throw new Error('Invalid terminal dimensions or cursor.');params[key==='waitMS'?'wait_ms':key]=input[key];}
 if(['input','resize','control','interrupt','close','handoff'].includes(input.operation)){
  if(!Number.isSafeInteger(input.controlRevision)||input.controlRevision<1)throw new Error('Read fresh terminal control state first.');params.control_revision=input.controlRevision;
 }
 if(['input','interrupt'].includes(input.operation)){if(!Number.isSafeInteger(input.sequence)||input.sequence<1)throw new Error('Invalid terminal input sequence.');params.sequence=input.sequence;}
 if(input.operation==='input'){
  if((typeof input.data==='string')===(typeof input.bytes==='string'))throw new Error('Supply one terminal input encoding.');
  if(input.data!==undefined){if(typeof input.data!=='string'||Buffer.byteLength(input.data,'utf8')>16384)throw new Error('Terminal input exceeds 16 KiB.');params.data=input.data;}
  if(input.bytes!==undefined){if(typeof input.bytes!=='string'||input.bytes.length>22000)throw new Error('Invalid terminal byte input.');const bytes=Buffer.from(input.bytes,'base64');if(bytes.length>16384||bytes.toString('base64')!==input.bytes)throw new Error('Invalid terminal byte input.');params.bytes=input.bytes;}
 }
 if(input.operation==='control'){if(input.release!==undefined&&typeof input.release!=='boolean')throw new Error('Invalid terminal control request.');params.release=input.release===true;}
 return {method:'terminal.'+input.operation,params};
}
module.exports={validateTerminal};
