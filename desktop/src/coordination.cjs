'use strict';
const id = value => {
  if (typeof value !== 'string' || !/^[\w-]{1,128}$/.test(value)) throw new Error('Invalid session or message ID.');
  return value;
};
function fields(input, allowed) {
  if (!input || typeof input !== 'object' || Array.isArray(input) || Object.keys(input).some(key => !allowed.includes(key))) throw new Error('Invalid coordination request.');
  id(input.sessionID);
}
function text(value, max, label) {
  if (typeof value !== 'string' || !value.trim() || Buffer.byteLength(value) > max) throw new Error(`Invalid ${label}.`);
  return value;
}
function contractSpec(value) {
  const keys = ['name','endpoint','schema_version','schema','acceptance_criteria','artifacts','owned_paths','dependencies','compatibility_changes','task_id','worktree'];
  const object = (item, allowed) => {if (!item || typeof item !== 'object' || Array.isArray(item) || Object.keys(item).some(key => !allowed.includes(key))) throw new Error('Invalid contract fields.');};
  object(value, keys);
  if (Buffer.byteLength(JSON.stringify(value)) > 60 * 1024) throw new Error('Contract exceeds the desktop 60 KiB limit.');
  text(value.name,256,'contract name'); text(value.endpoint,2048,'endpoint'); text(value.schema_version,128,'schema version');
  if (!value.schema || typeof value.schema !== 'object' || Array.isArray(value.schema)) throw new Error('Schema must be a JSON object.');
  if (!Array.isArray(value.acceptance_criteria) || !value.acceptance_criteria.length || value.acceptance_criteria.length > 64) throw new Error('Include acceptance criteria.');
  for (const criterion of value.acceptance_criteria) text(criterion,4096,'acceptance criterion');
  for (const [key,max] of [['artifacts',64],['owned_paths',128],['dependencies',128],['compatibility_changes',64]]) if (!Array.isArray(value[key]) || value[key].length > max) throw new Error('Invalid contract list.');
  for (const path of value.owned_paths) text(path,4096,'owned path');
  for (const item of value.artifacts) {object(item,['name','path','media_type','digest']);text(item.name,256,'artifact name');text(item.path,4096,'artifact path');for(const key of ['media_type','digest'])if(item[key]!==undefined && typeof item[key]!=='string')throw new Error('Invalid artifact metadata.');}
  for (const item of value.dependencies) {object(item,['contract_id','revision']);id(item.contract_id);if(!Number.isSafeInteger(item.revision)||item.revision<1)throw new Error('Invalid dependency revision.');}
  for (const item of value.compatibility_changes) {object(item,['kind','from_version','to_version','description']);if(!['additive','breaking','compatible'].includes(item.kind))throw new Error('Invalid compatibility kind.');text(item.from_version,128,'previous version');text(item.to_version,128,'next version');text(item.description,4096,'compatibility description');}
  for(const [key,max] of [['task_id',256],['worktree',1024]])if(value[key]!==undefined && (typeof value[key]!=='string'||Buffer.byteLength(value[key])>max))throw new Error('Invalid contract reference.');
  return value;
}
function validate(operation, input) {
  const allowed = {review: ['sessionID','messageID','revision','usageRevision','reviewID','note','tokenLimit','costLimit','requeue'], register: ['sessionID', 'name', 'role', 'repositoryScope'], peers: ['sessionID'], messages: ['sessionID', 'beforeID'], send: ['sessionID', 'to', 'message', 'key', 'taskID', 'tokenLimit', 'costLimit', 'conversationID', 'dependsOn', 'requiredContracts'], graph: ['sessionID', 'messageID'], cancel: ['sessionID', 'messageID', 'revision'], message: ['sessionID', 'messageID', 'afterRevision'], usage: ['sessionID', 'messageID'], escalations: ['sessionID', 'after'], contracts: ['sessionID'], readiness: ['sessionID', 'contractID'], acknowledge: ['sessionID', 'contractID', 'revision'], createContract: ['sessionID','spec'], updateContract: ['sessionID','contractID','revision','spec'], verification: ['sessionID'], evaluation: ['sessionID']}[operation];
  if (!allowed) throw new Error('Unsupported coordination operation.');
  fields(input, allowed);
  const result = {session_id: input.sessionID};
  if(operation==='review') {
    if(!Number.isSafeInteger(input.revision)||input.revision<1||!Number.isSafeInteger(input.usageRevision)||input.usageRevision<1)throw new Error('Refresh the request and budget revisions before review.');
    if(typeof input.messageID!=='string'||!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(input.messageID)||typeof input.reviewID!=='string'||!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(input.reviewID))throw new Error('Invalid review identity.');
    if(!Number.isSafeInteger(input.tokenLimit)||input.tokenLimit<0||input.tokenLimit>1e12||typeof input.costLimit!=='number'||!Number.isFinite(input.costLimit)||input.costLimit<0||input.costLimit>1e6||input.requeue!==true)throw new Error('Explicit limits and replay confirmation are required.');
    result.input={message_id:id(input.messageID),expected_revision:input.revision,expected_usage_revision:input.usageRevision,review_id:input.reviewID,note:text(input.note,2048,'review note'),limits:{tokens:input.tokenLimit,cost_usd:input.costLimit},requeue:true};
  }
  if (operation === 'register' || operation === 'send') {
    const name = operation === 'register' ? input.name : input.to;
    if (typeof name !== 'string' || !/^[a-z][a-z0-9_-]{0,63}$/.test(name)) throw new Error('Agent names must start with a lowercase letter and use lowercase letters, digits, hyphens or underscores.');
    if (operation === 'register') Object.assign(result, {name, role: text(input.role, 256, 'role (maximum 256 bytes)')});
    else Object.assign(result, {to: name, kind: 'request', payload: text(input.message, 16000, 'message (maximum 16,000 bytes)'), idempotency_key: id(input.key)});
  }
  if (operation === 'register' && input.repositoryScope !== undefined) {
    if(typeof input.repositoryScope !== 'boolean') throw new Error('Repository scope must be an explicit boolean.');
    result.repository_scope=input.repositoryScope;
  }
  if (operation === 'send') {
    if (input.requiredContracts !== undefined) {
      const pins=input.requiredContracts;
      if(!Array.isArray(pins)||pins.length>32||pins.some(pin=>!pin||typeof pin!=='object'||Array.isArray(pin)||Object.keys(pin).some(key=>!['contract_id','revision'].includes(key))||typeof pin.contract_id!=='string'||!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(pin.contract_id)||!Number.isSafeInteger(pin.revision)||pin.revision<1)||new Set(pins.map(pin=>pin.contract_id)).size!==pins.length)throw new Error('Required agreements need distinct UUIDs and positive revision numbers (maximum 32).');
      result.required_contracts=pins.map(pin=>({contract_id:pin.contract_id,revision:pin.revision}));
    }
    if (input.dependsOn !== undefined) {
      if (!Array.isArray(input.dependsOn) || input.dependsOn.length > 32 || new Set(input.dependsOn).size !== input.dependsOn.length || input.dependsOn.some(value => typeof value !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value))) throw new Error('Prerequisites must be at most 32 distinct request UUIDs.');
      result.depends_on = [...input.dependsOn];
    }
    if (input.taskID !== undefined) result.task_id = text(input.taskID, 128, 'task name');
    if (input.conversationID !== undefined) result.conversation_id = id(input.conversationID);
    if (input.tokenLimit !== undefined) {
      if (!Number.isSafeInteger(input.tokenLimit) || input.tokenLimit < 0 || input.tokenLimit > 1e12) throw new Error('Token limit must be a whole number from 0 to 1,000,000,000,000.');
    }
    if (input.costLimit !== undefined && (typeof input.costLimit !== 'number' || !Number.isFinite(input.costLimit) || input.costLimit < 0 || input.costLimit > 1e6)) throw new Error('Invalid cost limit.');
    if (input.tokenLimit !== undefined || input.costLimit !== undefined) {
      result.usage_budget = {tokens: input.tokenLimit ?? 0, cost_usd: input.costLimit ?? 0};
    }
  }
  if (operation === 'message' || operation === 'usage' || operation === 'graph') result.message_id = id(input.messageID);
  if (operation === 'graph') result.limit = 100;
  if (operation === 'message' || operation === 'escalations') {
    const cursor = operation === 'message' ? input.afterRevision : input.after;
    if (cursor !== undefined && (!Number.isSafeInteger(cursor) || cursor < 0)) throw new Error('Invalid activity cursor.');
    result[operation === 'message' ? 'after_revision' : 'after'] = cursor ?? 0;
    result.limit = 25;
  }
  if (operation === 'contracts' || operation === 'readiness') result.input = {action: operation === 'contracts' ? 'list' : 'readiness', ...(operation === 'readiness' ? {contract_id: id(input.contractID)} : {})};
  if (operation === 'acknowledge') {
    if (!Number.isSafeInteger(input.revision) || input.revision < 1) throw new Error('Invalid contract revision.');
    result.input = {action: 'acknowledge', contract_id: id(input.contractID), expected_revision: input.revision};
  }
  if (operation === 'createContract' || operation === 'updateContract') {
    result.input = {action: operation === 'createContract' ? 'create' : 'update', spec: contractSpec(input.spec)};
    if(operation === 'updateContract') {if(!Number.isSafeInteger(input.revision)||input.revision<1)throw new Error('Invalid contract revision.');Object.assign(result.input,{contract_id:id(input.contractID),expected_revision:input.revision});}
  }
  if (operation === 'messages') {
    result.limit = 50;
    if (input.beforeID !== undefined) result.before_id = id(input.beforeID);
  }
  if (operation === 'cancel') {
    if (!Number.isSafeInteger(input.revision) || input.revision < 1) throw new Error('Invalid message revision.');
    Object.assign(result, {message_id: id(input.messageID), expected_revision: input.revision});
  }
  return result;
}
const methods = Object.freeze({review: 'coordination.review', graph: 'coordination.graph', register: 'coordination.register', peers: 'coordination.peers', messages: 'coordination.messages', send: 'coordination.send', cancel: 'coordination.cancel', message: 'coordination.message', usage: 'coordination.usage', escalations: 'coordination.escalations', contracts: 'coordination.contract', readiness: 'coordination.contract', acknowledge: 'coordination.contract', createContract: 'coordination.contract', updateContract: 'coordination.contract', verification: 'tests.results', evaluation: 'self.eval.status'});
module.exports = {validate, methods};
