'use strict';
function validateQuestionAnswer(value,validateSession){
 if(!value||typeof value!=='object'||Array.isArray(value)||Object.keys(value).sort().join(',')!=='cancel,index,questionID,sessionID,text')throw new Error('Expected the selected chat and exact clarification answer.');
 const sid=validateSession(value.sessionID);
 if(typeof value.questionID!=='string'||!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value.questionID)||typeof value.text!=='string'||Buffer.byteLength(value.text)>8192||!Number.isSafeInteger(value.index)||value.index< -1||value.index>31||typeof value.cancel!=='boolean')throw new Error('Invalid clarification identity or answer.');
 if(value.cancel&&(value.text!==''||value.index!==-1))throw new Error('Cancelled clarification must not contain an answer.');
 if(!value.cancel&&value.index===-1&&!value.text.trim())throw new Error('Enter a clarification answer.');
 return{session_id:sid,question_id:value.questionID,text:value.text,index:value.index,cancel:value.cancel};
}
function validateNoInteractionInput(value){if(value!==undefined)throw new Error('Stop all agents does not accept parameters.');return{};}
module.exports={validateQuestionAnswer,validateNoInteractionInput};
