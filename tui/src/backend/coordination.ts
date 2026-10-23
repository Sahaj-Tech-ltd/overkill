import type { BackendClient } from './client.ts';

export interface RuntimePresence { state: string; source: string; observers: number; observed_at?: string; expires_at?: string }
export interface RepositoryInfo { scope: string; root: string; common_dir: string; head: string; branch: string }
export interface Peer { project_id?: string; workspace?: string; repository?: RepositoryInfo; id: string; name: string; role: string; enabled: boolean; session_id?: string; presence?: RuntimePresence }
export interface PublishedContract {
  id: string; owner_peer_id: string; revision: number;
  spec: {name:string; endpoint:string; schema_version:string; schema:unknown; workspace:string; worktree?:string; owned_paths:string[]; acceptance_criteria:string[]; artifacts:{name:string;path:string;media_type?:string;digest?:string}[]; dependencies:ContractPin[]; compatibility_changes:{kind:string;from_version:string;to_version:string;description:string}[];task_id?:string};
}
export function contractReviewLines(contract:PublishedContract):string[] {
  const spec=contract.spec;
  const safe=(value:unknown)=>String(value??'').replace(/[\x00-\x1f\x7f-\x9f]/g,'�');
  const schema=JSON.stringify(spec.schema);
  return [`${safe(spec.name)} · current r${contract.revision}`,`Owner: ${safe(contract.owner_peer_id)}`,`Endpoint: ${safe(spec.endpoint)}`,`Schema version: ${safe(spec.schema_version)}`,`Schema excerpt: ${safe(schema).slice(0,160)}${schema?.length>160?' [truncated]':''}`,`Workspace: ${safe(spec.workspace)}`,`Worktree: ${safe(spec.worktree||'same as workspace')}`,
    ...(spec.owned_paths??[]).map(path=>`Owns: ${safe(path)}`),...(spec.acceptance_criteria??[]).map(value=>`Acceptance: ${safe(value)}`),
    ...(spec.artifacts??[]).map(value=>`Artifact: ${safe(value.name)} · ${safe(value.path)}${value.media_type?' · '+safe(value.media_type):''}${value.digest?' · digest '+safe(value.digest):''}`),
    ...(spec.dependencies??[]).map(value=>`Contract dependency: ${safe(value.contract_id)}@${value.revision}`),
    ...(spec.compatibility_changes??[]).map(value=>`Compatibility: ${safe(value.kind)} ${safe(value.from_version)} → ${safe(value.to_version)} · ${safe(value.description)}`),
    'Published agreement/artifacts are declarations, not verification proof.'].flatMap(line=>line.match(/.{1,90}/gu)??['']);
}
export interface ContractPin { contract_id: string; revision: number }
export interface ContractBlocker { contract_id: string; reason: string; expected_revision?: number; actual_revision?: number }
export interface CoordinationMessage {
  id: string; from: string; to: string; kind: string; payload: unknown;
  state: string; revision: number; conversation_id: string; task_id?: string; depends_on?: string[]; required_contracts?: ContractPin[]; updated_at: string;
}
export interface DependencyGraph { root_id: string; nodes: {message: CoordinationMessage; readiness: 'ready' | 'waiting' | 'dependency_failed' | 'terminal' | 'active'; blockers: {id: string; state: string; reason: string}[]; contract_blockers?: ContractBlocker[]}[]; truncated: boolean; hidden_dependencies: boolean }
export interface MessagePage { messages: CoordinationMessage[]; has_more: boolean; before_id: string }
export interface Activity { revision: number; type: string; payload: unknown; created_at: string }
export interface TaskUsage {
  owner_peer_id?: string; revision?: number;
  scope: string; task_id?: string; limits: { tokens: number; cost_usd: number };
  input_tokens: number; output_tokens: number; cached_input_tokens: number; estimated_cost_usd: number;
  pending_executions: number; unconfirmed_executions: number; unknown_usage: number; unknown_price: number;
  requires_review: boolean; reason?: string;
}
export interface TaskDetail { message: CoordinationMessage; events: Activity[]; next_revision: number; has_more: boolean }
export interface ReviewInput { message_id: string; expected_revision: number; expected_usage_revision: number; review_id: string; note: string; limits: {tokens: number; cost_usd: number}; requeue: true }
export interface ReviewDraft { messageID: string; expectedRevision: number; expectedUsageRevision: number; note: string; tokens: string; cost: string; confirmed: boolean; frozen: ReviewInput | null }
export interface RequestDraft {
  to: string; message: string; taskID: string; tokens: string; cost: string;
  dependencies?: string; requiredContracts?: string; conversationID?: string; key: string; reused?: boolean;
}
export const stateLabel = (state: string): string => ({ accepted: 'queued', started: 'working' }[state] ?? state);
export function payloadText(payload: unknown): string {
  if (typeof payload === 'string') return payload;
  if (payload && typeof payload === 'object' && 'response' in payload && typeof payload.response === 'string') return payload.response;
  return JSON.stringify(payload) ?? '';
}
function required(value: string, max: number, name: string): string {
  if (!value.trim() || Buffer.byteLength(value) > max) throw new Error(`${name} is required (maximum ${max} bytes).`);
  return value;
}
function name(value: string): string {
  if (!/^[a-z][a-z0-9_-]{0,63}$/.test(value)) throw new Error('Use a lowercase agent name, digits, hyphens or underscores.');
  return value;
}
export function parseDependencies(value = ''): string[] {
  if (Buffer.byteLength(value) > 4096) throw new Error('Prerequisite request UUID list exceeds 4096 bytes.');
  const ids = value.trim() ? value.trim().split(/[\s,]+/) : [];
  if (ids.length > 32) throw new Error('Use at most 32 prerequisite request IDs.');
  if (ids.some(id => !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id))) throw new Error('Each prerequisite must be a complete request UUID.');
  const canonical = ids.map(id => id.toLowerCase());
  if (new Set(canonical).size !== ids.length) throw new Error('Prerequisite request IDs must be unique.');
  return canonical;
}
export function parseContractPins(value = ''): ContractPin[] {
  if (Buffer.byteLength(value) > 4096) throw new Error('Contract pin list exceeds 4096 bytes.');
  const entries = value.trim() ? value.trim().split(/[\s,]+/) : [];
  if (entries.length > 32) throw new Error('Use at most 32 required contracts.');
  const pins = entries.map(entry => {
    const match = /^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})@([1-9][0-9]*)$/i.exec(entry);
    if (!match || !Number.isSafeInteger(Number(match[2]))) throw new Error('Each contract pin must be a complete UUID@revision with a positive safe integer revision.');
    return {contract_id: match[1]!.toLowerCase(), revision: Number(match[2])};
  });
  if (new Set(pins.map(pin => pin.contract_id)).size !== pins.length) throw new Error('Required contract IDs must be unique.');
  return pins;
}
export function contractBlockerLabel(blocker: ContractBlocker): string {
  const reason = blocker.reason.startsWith('current_revision_not_acknowledged_by:')
    ? `Acknowledgement needed from ${blocker.reason.slice('current_revision_not_acknowledged_by:'.length)}`
    : ({owner_disabled: 'Contract owner disabled', dependency_revision_changed: 'Pinned dependency revision changed', required_contract_revision_changed: 'Required contract revision changed', required_contract_deleted: 'Required contract deleted', contract_deleted: 'Contract deleted', contract_revision_changed: 'Contract revision changed'} as Record<string, string>)[blocker.reason] ?? blocker.reason;
  return `${reason}${blocker.expected_revision ? ` · pinned r${blocker.expected_revision}` : ''}${blocker.actual_revision ? ` · current r${blocker.actual_revision}` : ''}`;
}
export class CoordinationClient {
  constructor(private backend: Pick<BackendClient, 'call'>) {}
  peers(session: string, signal?: AbortSignal) { return this.backend.call<{self: Peer | null; peers: Peer[]}>('coordination.peers', {session_id: session}, {signal}); }
  register(session: string, agent: string, role: string, signal?: AbortSignal, repositoryScope = false) { return this.backend.call<{peer: Peer}>('coordination.register', {session_id: session, name: name(agent), role: required(role, 256, 'Role'), ...(repositoryScope ? {repository_scope: true} : {})}, {signal}); }
  messages(session: string, before?: string, signal?: AbortSignal) { return this.backend.call<MessagePage>('coordination.messages', {session_id: session, limit: 50, ...(before ? {before_id: before} : {})}, {signal}); }
  detail(session: string, id: string, after = 0, signal?: AbortSignal) { return this.backend.call<TaskDetail>('coordination.message', {session_id: session, message_id: id, after_revision: after, limit: 25}, {signal}); }
  graph(session: string, id: string, signal?: AbortSignal) { return this.backend.call<DependencyGraph>('coordination.graph', {session_id: session, message_id: id, limit: 100}, {signal}); }
  usage(session: string, id: string, signal?: AbortSignal) { return this.backend.call<{usage: TaskUsage}>('coordination.usage', {session_id: session, message_id: id}, {signal}); }
  contracts(session:string,signal?:AbortSignal) { return this.backend.call<{result:PublishedContract[]}>('coordination.contract',{session_id:session,input:{action:'list'}},{signal}); }
  cancel(session: string, item: CoordinationMessage, signal?: AbortSignal) { return this.backend.call<{message: CoordinationMessage}>('coordination.cancel', {session_id: session, message_id: item.id, expected_revision: item.revision}, {signal}); }
  validateReview(input: ReviewInput) {
    const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
    if (!uuid.test(input.message_id) || !uuid.test(input.review_id)) throw new Error('Review and request IDs must be canonical UUIDs.');
    if (![input.expected_revision,input.expected_usage_revision].every(value => Number.isSafeInteger(value) && value > 0)) throw new Error('Refresh request and usage revisions before review.');
    if (input.requeue !== true) throw new Error('Requeue confirmation is required.');
    required(input.note, 2048, 'Review note');
    const {tokens,cost_usd} = input.limits;
    if (!Number.isSafeInteger(tokens) || tokens < 0 || tokens > 1e12 || !Number.isFinite(cost_usd) || cost_usd < 0 || cost_usd > 1e6) throw new Error('Explicit token and USD limits are invalid.');
  }
  review(session: string, input: ReviewInput, signal?: AbortSignal) {
    this.validateReview(input);
    const {tokens,cost_usd}=input.limits;
    return this.backend.call<{message: CoordinationMessage; usage: TaskUsage}>('coordination.review', {session_id: session,input:{message_id:input.message_id,expected_revision:input.expected_revision,expected_usage_revision:input.expected_usage_revision,review_id:input.review_id,note:input.note,limits:{tokens,cost_usd},requeue:true}}, {signal});
  }
  send(session: string, draft: RequestDraft, signal?: AbortSignal) {
    const tokens = draft.tokens === '' ? 0 : Number(draft.tokens), cost = draft.cost === '' ? 0 : Number(draft.cost);
    if (!Number.isSafeInteger(tokens) || tokens < 0 || tokens > 1e12) throw new Error('Token limit must be a whole number from 0 to 1,000,000,000,000.');
    if (!Number.isFinite(cost) || cost < 0 || cost > 1e6) throw new Error('Cost limit must be between 0 and 1,000,000 USD.');
    if (Buffer.byteLength(draft.taskID) > 128) throw new Error('Task name exceeds 128 bytes.');
    const dependencies = parseDependencies(draft.dependencies);
    const requiredContracts = parseContractPins(draft.requiredContracts);
    return this.backend.call<{message: CoordinationMessage}>('coordination.send', {
      session_id: session, to: name(draft.to), kind: 'request', payload: required(draft.message, 16000, 'Message'), idempotency_key: draft.key,
      ...(dependencies.length ? {depends_on: dependencies} : {}),
      ...(requiredContracts.length ? {required_contracts: requiredContracts} : {}),
      ...(draft.taskID ? {task_id: draft.taskID} : {}), ...(draft.conversationID ? {conversation_id: draft.conversationID} : {}),
      ...(draft.tokens !== '' || draft.cost !== '' ? {usage_budget: {tokens, cost_usd: cost}} : {}),
    }, {signal});
  }
}

export function presenceLabel(presence: RuntimePresence | undefined, now=Date.now()): string {
  if(presence?.source!=='runtime_lease'||!Number.isSafeInteger(presence.observers)||presence.observers<1||!Number.isFinite(Date.parse(presence.expires_at??''))||Date.parse(presence.expires_at??'')<=now)return 'Presence unknown';
  return ({ready:'Runtime idle',working:'Working',waiting_for_input:'Waiting for input',busy:'Finishing an operation',multiple_runtimes:'Multiple runtimes observed'} as Record<string,string>)[presence.state]??'Presence unknown';
}
