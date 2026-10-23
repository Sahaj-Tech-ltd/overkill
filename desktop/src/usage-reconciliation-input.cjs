'use strict';
const uuid=value=>typeof value==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value);
const inputKeys=['message_id','expected_message_revision','expected_usage_revision','expected_journal_fingerprint','reconciliation_id','input_tokens','output_tokens','cached_input_tokens','cost_usd','usage_known','price_known','reason','evidence','basis','acknowledge_uncertainty'];
function object(value,keys){if(!value||typeof value!=='object'||Array.isArray(value)||Object.keys(value).some(key=>!keys.includes(key)))throw new Error('Invalid usage reconciliation fields.');}
function validateUsageReconciliation(operation,value){
 const keys=operation==='write'?['sessionID','input']:operation==='candidates'?['sessionID','messageID','afterID']:['sessionID','messageID'];object(value,keys);
 if(typeof value.sessionID!=='string'||!/^[\w-]{1,128}$/.test(value.sessionID))throw new Error('Invalid reconciliation chat.');
 if(operation!=='write'){
  if(!uuid(value.messageID))throw new Error('Invalid reconciliation message.');
  if(operation==='candidates'&&value.afterID!==undefined&&value.afterID!==''&&!uuid(value.afterID))throw new Error('Invalid candidate cursor.');
  return {session_id:value.sessionID,message_id:value.messageID,...(operation==='candidates'?{after_id:value.afterID||'',limit:25}:{})};
 }
 const input=value.input;object(input,inputKeys);if(Object.keys(input).length!==inputKeys.length||!uuid(input.message_id)||!uuid(input.reconciliation_id)||![input.expected_message_revision,input.expected_usage_revision].every(n=>Number.isSafeInteger(n)&&n>0)||typeof input.expected_journal_fingerprint!=='string'||!/^[a-f0-9]{64}$/.test(input.expected_journal_fingerprint))throw new Error('Refresh the exact reconciliation evidence before submitting.');
 if(![input.input_tokens,input.output_tokens,input.cached_input_tokens].every(n=>Number.isSafeInteger(n)&&n>=0&&n<=1e12)||input.cached_input_tokens>input.input_tokens||!Number.isFinite(input.cost_usd)||input.cost_usd<0||input.cost_usd>1e6)throw new Error('Enter explicit valid token totals and cost.');
 for(const [key,max]of[['reason',2048],['evidence',8192]])if(typeof input[key]!=='string'||!input[key].trim()||Buffer.byteLength(input[key])>max)throw new Error('Provide a bounded reason and evidence.');
 if(!['external_record','conservative_estimate'].includes(input.basis)||input.usage_known!==true||input.price_known!==true||input.acknowledge_uncertainty!==true)throw new Error('Choose an evidence basis and explicitly attest accounting uncertainty.');
 return {session_id:value.sessionID,input:structuredClone(input)};
}
module.exports={validateUsageReconciliation};
