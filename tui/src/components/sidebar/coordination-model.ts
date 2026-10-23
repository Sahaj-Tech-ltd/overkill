import {UsageReconciliationClient,type ReconciliationDraft,type ReconciliationInput} from '../../backend/usage-reconciliation.ts';
import { randomUUID } from 'node:crypto';
import { CoordinationClient, type PublishedContract, type Peer, type MessagePage, type CoordinationMessage, type TaskDetail, type TaskUsage, type RequestDraft, type DependencyGraph, type ReviewDraft, type ReviewInput } from '../../backend/coordination.ts';
import type { BackendClient } from '../../backend/client.ts';

interface Snapshot {
  session: string; self: Peer | null; peers: Peer[]; page: MessagePage; detail: TaskDetail | null;
  contracts: PublishedContract[] | null; graph: DependencyGraph | null; usage: TaskUsage | null; draft: RequestDraft | null; review: ReviewDraft | null; reconciliation: ReconciliationDraft|null; busy: boolean; error: string | null;
}
const empty = (session = ''): Snapshot => ({session, self: null, peers: [], page: {messages: [], has_more: false, before_id: ''}, detail: null, contracts:null, graph: null, usage: null, draft: null, review: null, reconciliation:null, busy: false, error: null});
export class CoordinationModel {
  private state = empty();
  private listeners = new Set<() => void>();
  private controller = new AbortController();
  private generation = 0;
  private client: CoordinationClient;
  private accounting: UsageReconciliationClient;
  constructor(backend: Pick<BackendClient, 'call'>) { this.client = new CoordinationClient(backend);this.accounting=new UsageReconciliationClient(backend); }
  getSnapshot = (): Snapshot => this.state;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private update(patch: Partial<Snapshot>) { this.state = {...this.state, ...patch}; this.listeners.forEach((listener) => listener()); }
  dispose() { this.generation++; this.controller.abort(); }
  async setSession(session: string) {
    this.dispose(); this.controller = new AbortController(); this.state = empty(session); this.update({});
    if (session) await this.reload();
  }
  private async run(work: (session: string, signal: AbortSignal, commit: (patch: Partial<Snapshot>) => void) => Promise<void>) {
    if (!this.state.session || this.state.busy) return;
    const generation = this.generation;
    const session = this.state.session, signal = this.controller.signal;
    const commit = (patch: Partial<Snapshot>) => { if (generation === this.generation && !signal.aborted) this.update(patch); };
    commit({busy: true, error: null});
    try { await work(session, signal, commit); }
    catch (error) { commit({error: (error as Error).message}); }
    finally { commit({busy: false}); }
  }
  reload(older = false) {
    return this.run(async (session, signal, commit) => {
      const peers = await this.client.peers(session, signal);
      if (signal.aborted) return;
      commit(peers);
      if (!peers.self) { commit({page: empty().page, detail: null, graph: null, usage: null}); return; }
      const page = await this.client.messages(session, older ? this.state.page.before_id : undefined, signal);
      commit({page: {...page, messages: older ? [...this.state.page.messages, ...page.messages.filter((m) => !this.state.page.messages.some((old) => old.id === m.id))] : page.messages}});
      const selected = this.state.detail;
      if (!older && selected) {
        const [detail, usage, graph] = await Promise.all([this.client.detail(session, selected.message.id, 0, signal), this.client.usage(session, selected.message.id, signal), this.client.graph(session, selected.message.id, signal)]);
        commit({detail, graph, usage: usage.usage});
      }
    });
  }
  register(name: string, role: string, repositoryScope = false) {
    return this.run(async (session, signal, commit) => {
      await this.client.register(session, name, role, signal, repositoryScope);
      const peers = await this.client.peers(session, signal); commit(peers);
      if (peers.self) commit({page: await this.client.messages(session, undefined, signal)});
    });
  }
  inspect(item: CoordinationMessage, more = false) {
    return this.run(async (session, signal, commit) => {
      if (!more) commit({detail: null, graph: null, usage: null});
      const previous = more ? this.state.detail : null;
      const [detail, usage, graph] = await Promise.all([this.client.detail(session, item.id, previous?.next_revision ?? 0, signal), this.client.usage(session, item.id, signal), this.client.graph(session, item.id, signal)]);
      commit({detail: {...detail, events: [...(previous?.events ?? []), ...detail.events]}, graph, usage: usage.usage});
    });
  }
  cancel(item: CoordinationMessage) {
    return this.run(async (session, signal, commit) => {
      if (item.from !== this.state.self?.id || !['accepted','started','blocked'].includes(item.state)) throw new Error('Only your queued, working or blocked requests can be cancelled.');
      const {message} = await this.client.cancel(session, item, signal);
      commit({page: {...this.state.page, messages: this.state.page.messages.map((m) => m.id === message.id ? message : m)}, detail: null, graph: null, usage: null});
    });
  }
  loadContracts() {
    return this.run(async(session,signal,commit)=>{
      const {result}=await this.client.contracts(session,signal);
      commit({contracts:result??[]});
    });
  }
  closeDetail() { if (!this.state.busy) this.update({detail: null, graph: null, usage: null}); }
  canReview() {
    const {self,detail,usage} = this.state;
    return !!(self?.enabled && detail?.message.kind === 'request' && detail.message.state === 'blocked' && usage?.owner_peer_id === self.id && [detail.message.from,detail.message.to].includes(self.id) && Number.isSafeInteger(usage.revision) && usage.revision! > 0);
  }
  beginReview() {
    if (this.state.busy || !this.canReview()) return;
    const {detail,usage} = this.state;
    this.update({review:{messageID:detail!.message.id,expectedRevision:detail!.message.revision,expectedUsageRevision:usage!.revision!,tokens:String(usage!.limits.tokens),cost:String(usage!.limits.cost_usd),note:'',confirmed:false,frozen:null},error:null});
  }
  editReview(field: 'tokens' | 'cost' | 'note', value: string) {
    const review=this.state.review;
    if (!review || review.frozen || this.state.busy) return;
    this.update({review:{...review,[field]:value,confirmed:false}});
  }
  confirmReview() {
    const review=this.state.review;
    if (!review || review.frozen || this.state.busy) return;
    this.update({review:{...review,confirmed:!review.confirmed}});
  }
  discardReview() { if (!this.state.busy) this.update({review:null}); }
  submitReview() {
    return this.run(async (session,signal,commit) => {
      const review=this.state.review;
      if (!review || !review.confirmed) throw new Error('Explicitly confirm that replay may repeat prior effects.');
      if (!review.tokens.trim() || !review.cost.trim()) throw new Error('Enter explicit token and USD limits (0 means unlimited).');
      // Freeze before transport; an ambiguous result can only be retried with
      // exactly this body and review ID, never silently refreshed revisions.
      const input: ReviewInput = review.frozen ?? Object.freeze({message_id:review.messageID,expected_revision:review.expectedRevision,expected_usage_revision:review.expectedUsageRevision,review_id:randomUUID(),note:review.note,limits:Object.freeze({tokens:Number(review.tokens),cost_usd:Number(review.cost)}),requeue:true as const});
      this.client.validateReview(input);
      commit({review:{...review,frozen:input}});
      const {message,usage}=await this.client.review(session,input,signal);
      commit({review:null,usage,graph:null,detail:this.state.detail ? {...this.state.detail,message} : null,page:{...this.state.page,messages:this.state.page.messages.map(item=>item.id===message.id ? message : item)}});
    });
  }
  beginReconciliation() {
    return this.run(async(session,signal,commit)=>{
      const rootID=this.state.detail?.message.id;if(!rootID)throw new Error('Inspect a request first.');
      const page=await this.accounting.candidates(session,rootID,'',signal);if(signal.aborted)return;
      const targetID=page.candidates?.[0]?.message_id??rootID;
      const {snapshot}=await this.accounting.snapshot(session,targetID,signal);
      commit({reconciliation:{rootID,targetID,page,snapshot,cursor:'',cursors:[],input:'',output:'',cached:'',cost:'',basis:'',reason:'',evidence:'',confirmed:false,needsRefresh:false,frozen:null,receipt:null}});
    });
  }
  chooseReconciliationCandidate(direction:number) {
    return this.run(async(session,signal,commit)=>{
      const draft=this.state.reconciliation;if(!draft||draft.frozen)return;
      const list=draft.page?.candidates??[];if(!list.length)return;
      const index=Math.max(0,list.findIndex(item=>item.message_id===draft.targetID));const candidate=list[(index+direction+list.length)%list.length]!;
      const {snapshot}=await this.accounting.snapshot(session,candidate.message_id,signal);
      commit({reconciliation:{...draft,targetID:candidate.message_id,snapshot,input:'',output:'',cached:'',cost:'',basis:'',reason:'',evidence:'',confirmed:false,needsRefresh:false,receipt:null}});
    });
  }
  pageReconciliation(back=false) {
    return this.run(async(session,signal,commit)=>{
      const draft=this.state.reconciliation;if(!draft||draft.frozen)return;
      if(back&&!draft.cursors.length||!back&&!draft.page?.truncated)return;
      const cursor=back?draft.cursors.at(-1)!:draft.page!.next_cursor;
      const page=await this.accounting.candidates(session,draft.rootID,cursor,signal);if(signal.aborted)return;
      const targetID=page.candidates?.[0]?.message_id??draft.rootID;
      const {snapshot}=await this.accounting.snapshot(session,targetID,signal);
      commit({reconciliation:{...draft,page,targetID,snapshot,cursor,cursors:back?draft.cursors.slice(0,-1):[...draft.cursors,draft.cursor],input:'',output:'',cached:'',cost:'',basis:'',reason:'',evidence:'',confirmed:false,needsRefresh:false,receipt:null}});
    });
  }
  refreshReconciliation() {
    return this.run(async(session,signal,commit)=>{
      const draft=this.state.reconciliation;if(!draft||draft.frozen)return;
      const {snapshot}=await this.accounting.snapshot(session,draft.targetID,signal);
      commit({reconciliation:{...draft,snapshot,confirmed:false,needsRefresh:false,frozen:null,receipt:null}});
    });
  }
  editReconciliation(field:'input'|'output'|'cached'|'cost'|'basis'|'reason'|'evidence',value:string) {
    const draft=this.state.reconciliation;if(!draft||this.state.busy||draft.frozen||draft.receipt||!draft.snapshot?.can_reconcile)return;
    if(field==='basis'&&!['','external_record','conservative_estimate'].includes(value))return;
    this.update({reconciliation:{...draft,[field]:value,confirmed:false}});
  }
  confirmReconciliation(){const draft=this.state.reconciliation;if(draft&&!draft.frozen&&!draft.needsRefresh&&draft.snapshot?.can_reconcile)this.update({reconciliation:{...draft,confirmed:!draft.confirmed}});}
  discardReconciliation(){if(!this.state.busy)this.update({reconciliation:null});}
  submitReconciliation() {
    return this.run(async(session,signal,commit)=>{
      const draft=this.state.reconciliation,snapshot=draft?.snapshot;
      if(!draft||!snapshot?.can_reconcile||draft.needsRefresh||!draft.confirmed)throw new Error('Refresh evidence and explicitly attest manual accounting before submitting.');
      if(![draft.input,draft.output,draft.cached,draft.cost].every(value=>value.trim()))throw new Error('Enter every total explicitly; missing usage is not zero.');
      if(!draft.basis)throw new Error('Choose an evidence basis.');
      const input:ReconciliationInput=draft.frozen??{message_id:snapshot.message_id,expected_message_revision:snapshot.message_revision,expected_usage_revision:snapshot.usage_revision,expected_journal_fingerprint:snapshot.journal_fingerprint,reconciliation_id:randomUUID(),input_tokens:Number(draft.input),output_tokens:Number(draft.output),cached_input_tokens:Number(draft.cached),cost_usd:Number(draft.cost),usage_known:true,price_known:true,reason:draft.reason.trim(),evidence:draft.evidence.trim(),basis:draft.basis,acknowledge_uncertainty:true};
      this.accounting.validate(input);
      if((['input_tokens','output_tokens','cached_input_tokens'] as const).some(name=>input[name]<Math.max(snapshot.observed[name],snapshot.contribution[name]))||input.cost_usd<Math.max(snapshot.known_cost_floor_usd,input.basis==='conservative_estimate'?snapshot.contribution.estimated_cost_usd:0))throw new Error('Totals cannot reduce observed/attested tokens or the cost floor for this evidence basis.');
      commit({reconciliation:{...draft,frozen:Object.freeze(input)}});
      const result=await this.accounting.reconcile(session,input,signal);
      if(!result.committed){commit({reconciliation:{...draft,frozen:null,needsRefresh:true,confirmed:false},error:'Evidence changed. Nothing committed. Refresh evidence; your entries are retained.'});return;}
      const committed={...draft,snapshot:result.snapshot,frozen:null,confirmed:false,receipt:result.reconciliation,historical:true};
      commit({reconciliation:committed});
      try{
        const rootDetailID=this.state.detail?.message.id;
        const [latest,page,usage,detail]=await Promise.all([this.accounting.snapshot(session,draft.targetID,signal),this.accounting.candidates(session,draft.rootID,'',signal),this.client.usage(session,draft.rootID,signal),rootDetailID?this.client.detail(session,rootDetailID,0,signal):Promise.resolve(null)]);
        commit({usage:usage.usage,...(detail?{detail}:{}),reconciliation:{...committed,snapshot:latest.snapshot,page,cursor:'',cursors:[],historical:false}});
      }catch(error){commit({error:'Accounting committed; current accounting refresh failed. Immutable receipt is historical evidence: '+(error as Error).message});}
    });
  }
  newDraft() {
    const peer = this.state.peers.find((p) => p.id !== this.state.self?.id && p.enabled);
    if (!this.state.self || !peer || this.state.busy) return;
    this.update({draft: {to: peer.name, message: '', taskID: '', tokens: '', cost: '', dependencies: '', requiredContracts: '', key: randomUUID()}, error: null});
  }
  editDraft(field: 'to' | 'message' | 'taskID' | 'tokens' | 'cost' | 'dependencies' | 'requiredContracts', value: string) {
    if (!this.state.draft || this.state.busy || (this.state.draft.reused && ['to','taskID','tokens','cost','dependencies','requiredContracts'].includes(field))) return;
    this.update({draft: {...this.state.draft, [field]: value, key: randomUUID()}});
  }
  discardDraft() { if (!this.state.busy) this.update({draft: null}); }
  retry(item: CoordinationMessage) {
    return this.run(async (session, signal, commit) => {
      if (item.from !== this.state.self?.id || item.kind !== 'request' || !['blocked','failed','cancelled'].includes(item.state)) throw new Error('This request cannot be copied to a retry draft.');
      if (typeof item.payload !== 'string') throw new Error('Structured requests cannot be edited in this text composer.');
      const {usage} = await this.client.usage(session, item.id, signal);
      const peer = this.state.peers.find((p) => p.id === item.to && p.enabled);
      if (!peer) throw new Error('Recipient is unavailable. Refresh peers.');
      commit({draft: {to: peer.name, message: item.payload, taskID: item.task_id ?? '', conversationID: item.conversation_id, dependencies: (item.depends_on ?? []).join(', '), requiredContracts: (item.required_contracts ?? []).map(pin => `${pin.contract_id}@${pin.revision}`).join(', '),
        tokens: String(usage.limits.tokens), cost: String(usage.limits.cost_usd), reused: true, key: randomUUID()}});
    });
  }
  send() {
    return this.run(async (session, signal, commit) => {
      const draft = this.state.draft;
      if (!draft || !this.state.peers.some((p) => p.name === draft.to && p.enabled && p.id !== this.state.self?.id)) throw new Error('Choose an enabled recipient.');
      const {message} = await this.client.send(session, draft, signal);
      commit({draft: null, page: {...this.state.page, messages: [message, ...this.state.page.messages.filter((m) => m.id !== message.id)]}});
    });
  }
}
