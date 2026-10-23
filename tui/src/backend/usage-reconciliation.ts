import type {BackendClient} from './client.ts';
import type {TaskUsage} from './coordination.ts';
export interface ReconciliationTotals {input_tokens:number;output_tokens:number;cached_input_tokens:number;estimated_cost_usd:number;unknown_usage:number;unknown_price:number}
export interface ReconciliationRecord {reconciliation_id:string;message_id:string;actor_peer_id:string;basis:string;reason:string;evidence:string;provenance:'manual_attestation';before:ReconciliationTotals;after:ReconciliationTotals;created_at:string}
export interface ReconciliationSnapshot {message_id:string;message_revision:number;usage_revision:number;scope:string;owner_peer_id:string;journal_fingerprint:string;can_reconcile:boolean;reason:string;runs:{owner_id:string;finished:boolean;resolved:boolean}[];run_count:number;runs_truncated:boolean;samples:{key:string;input_tokens:number;output_tokens:number;cached_input_tokens:number;estimated_cost_usd:number;usage_known:boolean;price_known:boolean}[];sample_count:number;samples_truncated:boolean;observed:ReconciliationTotals;contribution:ReconciliationTotals;known_cost_floor_usd:number;previous:ReconciliationRecord[];previous_count:number;previous_truncated:boolean}
export interface ReconciliationCandidate {message_id:string;message_revision:number;state:string;pending_executions:number;unconfirmed_executions:number;can_reconcile:boolean;reason:string;unknown_usage:number;unknown_price:number}
export interface ReconciliationCandidates {scope:string;candidates:ReconciliationCandidate[];next_cursor:string;truncated:boolean}
export interface ReconciliationInput {message_id:string;expected_message_revision:number;expected_usage_revision:number;expected_journal_fingerprint:string;reconciliation_id:string;input_tokens:number;output_tokens:number;cached_input_tokens:number;cost_usd:number;usage_known:true;price_known:true;reason:string;evidence:string;basis:'external_record'|'conservative_estimate';acknowledge_uncertainty:true}
export type ReconciliationResult={committed:false;conflict:true}|{committed:true;usage:TaskUsage;snapshot:ReconciliationSnapshot;reconciliation:ReconciliationRecord};
export interface ReconciliationDraft {rootID:string;targetID:string;snapshot:ReconciliationSnapshot|null;page:ReconciliationCandidates|null;cursor:string;cursors:string[];input:string;output:string;cached:string;cost:string;basis:''|'external_record'|'conservative_estimate';reason:string;evidence:string;confirmed:boolean;needsRefresh:boolean;frozen:ReconciliationInput|null;receipt:ReconciliationRecord|null;historical?:boolean}
const uuid=(value:string)=>/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value);
export class UsageReconciliationClient {
 constructor(private backend:Pick<BackendClient,'call'>){}
 candidates(session:string,root:string,after='',signal?:AbortSignal){return this.backend.call<ReconciliationCandidates>('coordination.reconciliation.candidates',{session_id:session,message_id:root,after_id:after,limit:25},{signal});}
 snapshot(session:string,id:string,signal?:AbortSignal){return this.backend.call<{snapshot:ReconciliationSnapshot}>('coordination.reconciliation',{session_id:session,message_id:id},{signal});}
 validate(input:ReconciliationInput){
  if(!uuid(input.message_id)||!uuid(input.reconciliation_id)||![input.expected_message_revision,input.expected_usage_revision].every(n=>Number.isSafeInteger(n)&&n>0)||!/^[a-f0-9]{64}$/.test(input.expected_journal_fingerprint))throw new Error('Refresh the exact message/usage evidence before attesting.');
  if(![input.input_tokens,input.output_tokens,input.cached_input_tokens].every(n=>Number.isSafeInteger(n)&&n>=0&&n<=1e12)||input.cached_input_tokens>input.input_tokens||!Number.isFinite(input.cost_usd)||input.cost_usd<0||input.cost_usd>1e6)throw new Error('Explicit token totals and estimated cost are invalid.');
  for(const [text,max]of[[input.reason,2048],[input.evidence,8192]] as const)if(!text.trim()||Buffer.byteLength(text)>max)throw new Error('A bounded reason and evidence are required.');
  if(!['external_record','conservative_estimate'].includes(input.basis)||input.usage_known!==true||input.price_known!==true||input.acknowledge_uncertainty!==true)throw new Error('Evidence basis and explicit manual attestation are required.');
 }
 reconcile(session:string,input:ReconciliationInput,signal?:AbortSignal){this.validate(input);return this.backend.call<ReconciliationResult>('coordination.reconcile',{session_id:session,input},{signal});}
}
