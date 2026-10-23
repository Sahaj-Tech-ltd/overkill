import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { Box, Text, useInput, type Key } from 'ink';
import type { BackendClient } from '../../backend/client.ts';
import { payloadText, stateLabel, presenceLabel, contractBlockerLabel, contractReviewLines } from '../../backend/coordination.ts';
import { useTheme } from '../../hooks/use-theme.ts';
import { CoordinationModel } from './coordination-model.ts';

const fields = ['to', 'taskID', 'tokens', 'cost', 'dependencies', 'requiredContracts', 'message'] as const;
const labels = ['Recipient', 'Task', 'Token limit (0 unlimited)', 'USD limit (0 unlimited)', 'Prerequisite request UUIDs (comma separated)', 'Required contracts (UUID@revision)', 'Message'];
export function CoordinationPanel({backend, sessionId, active}: {backend: BackendClient; sessionId: string; active: boolean}): React.JSX.Element {
  const {theme} = useTheme();
  const model = useMemo(() => new CoordinationModel(backend), [backend]);
  const snapshot = useSyncExternalStore(model.subscribe, model.getSnapshot);
  const state = snapshot.session === sessionId ? snapshot : null;
  const [presenceNow,setPresenceNow] = useState(Date.now());
  const [index, setIndex] = useState(0);
  const [field, setField] = useState(0);
  const [inspectTab, setInspectTab] = useState(0);
  const [contractIndex,setContractIndex]=useState(0);
  const [contractOffset,setContractOffset]=useState(0);
  const [activityOffset, setActivityOffset] = useState(0);
  const [graphOffset, setGraphOffset] = useState(0);
  const [registration, setRegistration] = useState<{name: string; role: string; repositoryScope: boolean} | null>(null);
  const [registerField, setRegisterField] = useState(0);
  const [reviewField,setReviewField] = useState(0);
  const [reconciliationField,setReconciliationField]=useState(0);
  useEffect(() => {
    setContractIndex(0);setContractOffset(0);setIndex(0); setRegistration(null); setField(0); setInspectTab(0); setActivityOffset(0); setGraphOffset(0); setReviewField(0);setReconciliationField(0);
    void model.setSession(sessionId);
    const expiryTimer=setInterval(()=>setPresenceNow(Date.now()),1000);
    const timer = setInterval(() => {
      const current = model.getSnapshot();
      if (!current.draft && !current.review && !current.reconciliation && !current.detail && !current.busy) void model.reload();
    }, 4000);
    return () => {clearInterval(timer); clearInterval(expiryTimer); model.dispose();};
  }, [model, sessionId]);
  const messages = state?.page.messages ?? [];
  const selected = messages[Math.min(index, Math.max(0, messages.length - 1))];
  const draft = state?.draft;
  const detail = state?.detail?.message.id === selected?.id ? state?.detail : null;
  const review = state?.review;
  const reconciliation=state?.reconciliation;
  useEffect(()=>{
    if(inspectTab!==4||!detail)return;
    setContractIndex(0);setContractOffset(0);void model.loadContracts();
  },[inspectTab,detail?.message.id,model]);
  const inputHandler=(input:string,key:Key) => {
    if (!active || !state || state.busy || key.tab || (key.ctrl && input !== 'u') || (key.meta && !key.escape)) return;
    if(reconciliation){
      if(key.escape){model.discardReconciliation();return;}
      if(reconciliation.receipt)return;
      if(reconciliation.frozen){if(key.return)void model.submitReconciliation();return;}
      if(key.leftArrow){setReconciliationField(Math.max(0,reconciliationField-1));return;}
      if(key.rightArrow){setReconciliationField(Math.min(8,reconciliationField+1));return;}
      if(reconciliationField===0){if(input==='j'||key.downArrow)void model.chooseReconciliationCandidate(1);else if(input==='k'||key.upArrow)void model.chooseReconciliationCandidate(-1);else if(input==='n')void model.pageReconciliation();else if(input==='p')void model.pageReconciliation(true);else if(input==='r')void model.refreshReconciliation();else if(key.return)setReconciliationField(1);return;}
      if(key.return){if(reconciliationField<8)setReconciliationField(reconciliationField+1);else void model.submitReconciliation();return;}
      if(reconciliationField===8){if(input==='c')model.confirmReconciliation();return;}
      if(reconciliationField===5){if(input==='e')model.editReconciliation('basis','external_record');if(input==='m')model.editReconciliation('basis','conservative_estimate');return;}
      const name=({1:'input',2:'output',3:'cached',4:'cost',6:'reason',7:'evidence'} as const)[reconciliationField as 1|2|3|4|6|7];
      const value=reconciliation[name];model.editReconciliation(name,key.ctrl&&input==='u'?'':key.backspace||key.delete?value.slice(0,-1):value+input.replace(/[\r\n\x00-\x1f\x7f]/g,''));return;
    }
    if (review) {
      if (key.escape) {model.discardReview(); return;}
      if (review.frozen) {if(key.return)void model.submitReview();return;}
      if (key.leftArrow) {setReviewField(Math.max(0,reviewField-1));return;}
      if (key.rightArrow) {setReviewField(Math.min(3,reviewField+1));return;}
      if (key.return) {if(reviewField<3)setReviewField(reviewField+1);else void model.submitReview();return;}
      if(reviewField===3){if(input==='c')model.confirmReview();return;}
      const name=(['tokens','cost','note'] as const)[reviewField]!;
      const value=review[name];
      model.editReview(name,key.ctrl&&input==='u'?'':key.backspace||key.delete?value.slice(0,-1):value+input.replace(/[\r\n\x00-\x1f\x7f]/g,''));
      return;
    }
    if (registration) {
      if (key.escape) {setRegistration(null); return;}
      if (key.return) {
        if (registerField < 2) setRegisterField(registerField + 1);
        else void model.register(registration.name, registration.role, registration.repositoryScope).then(() => {if (model.getSnapshot().self) setRegistration(null);});
        return;
      }
      if (key.leftArrow) {setRegisterField(Math.max(0, registerField - 1)); return;}
      if (registerField === 2) {if(input==='s')setRegistration({...registration,repositoryScope:!registration.repositoryScope});return;}
      const field = registerField === 0 ? 'name' : 'role';
      const value = registration[field];
      setRegistration({...registration, [field]: key.ctrl && input === 'u' ? '' : key.backspace || key.delete ? value.slice(0, -1) : value + input.replace(/[\r\n\x00-\x1f\x7f]/g, '')});
      return;
    }
    if (draft) {
      if (key.escape) {model.discardDraft(); return;}
      if (key.return) {if (field < fields.length) setField(field + 1); else void model.send(); return;}
      if (key.leftArrow) {setField(Math.max(0, field - 1)); return;}
      if (key.rightArrow) {setField(Math.min(fields.length, field + 1)); return;}
      const name = fields[field];
      if (!name) return;
      const value = draft[name] ?? '';
      model.editDraft(name, key.ctrl && input === 'u' ? '' : key.backspace || key.delete ? value.slice(0, -1) : value + input.replace(/[\r\n\x00-\x1f\x7f]/g, ''));
      return;
    }
    if (input === 'r') {void model.reload().then(()=>{if(inspectTab===4)void model.loadContracts();}); return;}
    if (input === 'g' && !state.self) {setRegistration({name: '', role: '', repositoryScope:false}); setRegisterField(0); return;}
    if (input === 'o' && state.page.has_more) {void model.reload(true); return;}
    if (detail && key.escape) {model.closeDetail(); return;}
    if (detail && input === 'v') {setInspectTab(previous=>(previous+1)%5);return;}
    if(detail&&inspectTab===4){
      const contracts=state.contracts??[],lines=contracts[contractIndex]?contractReviewLines(contracts[contractIndex]!):[];
      if(input==='n'||input==='p'){setContractIndex(Math.max(0,Math.min(contracts.length-1,contractIndex+(input==='n'?1:-1))));setContractOffset(0);return;}
      if(input==='j'||key.downArrow){setContractOffset(Math.min(Math.max(0,lines.length-8),contractOffset+1));return;}
      if(input==='k'||key.upArrow){setContractOffset(Math.max(0,contractOffset-1));return;}
    }
    if (input === 'n') {model.newDraft(); setField(0); return;}
    if(detail&&inspectTab===1&&input==='u'){setReconciliationField(0);void model.beginReconciliation();return;}
    if (detail && inspectTab === 1 && input === 'b') {model.beginReview();setReviewField(0);return;}
    if (detail && inspectTab === 3 && (input === 'j' || key.downArrow)) {setGraphOffset(Math.min(graphOffset + 1, Math.max(0, (state.graph?.nodes.length ?? 0) - 3))); return;}
    if (detail && inspectTab === 3 && (input === 'k' || key.upArrow)) {setGraphOffset(Math.max(0, graphOffset - 1)); return;}
    if (detail && (input === 'j' || key.downArrow)) {setActivityOffset(Math.min(activityOffset + 1, Math.max(0, detail.events.length - 4))); return;}
    if (detail && (input === 'k' || key.upArrow)) {setActivityOffset(Math.max(0, activityOffset - 1)); return;}
    if (input === 'j' || key.downArrow) {setIndex(Math.min(index + 1, messages.length - 1)); return;}
    if (input === 'k' || key.upArrow) {setIndex(Math.max(0, index - 1)); return;}
    if (key.escape) {setIndex(0); return;}
    if (!selected) return;
    if (key.return) {setInspectTab(0); setActivityOffset(0); setGraphOffset(0); void model.inspect(selected); return;}
    if (input === 'a' && detail?.message.id === selected.id && detail.has_more) {void model.inspect(selected, true); return;}
    if (input === 'x') {void model.cancel(selected); return;}
    if (input === 'y') {setField(fields.length); void model.retry(selected);}
  };
  const inputHandlerRef=useRef<(input:string,key:Key)=>void>(()=>{});
  // Ink replaces useInput listeners in a passive effect. Publish the committed
  // handler during layout so a visible new form cannot use the previous form's
  // callback while that passive replacement is pending.
  useLayoutEffect(()=>{inputHandlerRef.current=inputHandler;});
  const forwardInput=useCallback((input:string,key:Key)=>{inputHandlerRef.current(input,key);},[]);
  useInput(forwardInput,{isActive:active});
  const names = new Map(state?.peers.map((p) => [p.id, p.name]));
  const visible = messages.slice(Math.max(0, index - 3), Math.max(0, index - 3) + 6);
  const usage = state?.usage;
  return <Box flexDirection="column" overflow="hidden">
    <Text color={theme.accent} bold>Agent coordination</Text>
    {!sessionId && <Text dimColor>Select a saved chat.</Text>}
    {state?.busy && <Text dimColor>Loading / saving…</Text>}
    {state?.error && <Text color={theme.error}>{state.error} · r:refresh</Text>}
    {state?.self ? <Text>As {state.self.name} · {state.self.role} · {state.self.project_id?.startsWith('repository:') ? 'Repository worktrees' : 'This checkout'}</Text> : sessionId && <Text dimColor>This chat is unregistered · g:register</Text>}
    {!detail && !draft && !registration && <Text dimColor>Runtime presence expires; registration is separate.</Text>}
    {!detail && !draft && !registration && state?.peers.slice(0, 4).map((peer) => <Text key={peer.id} wrap="truncate-end">{peer.name}: {presenceLabel(peer.presence,presenceNow)} · {peer.role}</Text>)}
    {!detail && !draft && !registration && (state?.peers.length ?? 0) > 4 && <Text dimColor>{state!.peers.length - 4} more peers; type recipient in draft.</Text>}
    {registration && <Box flexDirection="column">
      <Text bold>{registerField === 2 ? 'Review registration' : registerField === 0 ? 'Agent name:' : 'Role:'}</Text>
      <Text>Name: {registration.name}</Text><Text>Role: {registration.role}</Text><Text>Scope: {registration.repositoryScope ? 'Repository worktrees (explicit opt-in)' : 'This checkout'}</Text>{registerField === 2 && <Text dimColor>s:toggle scope · Git checkout root required for repository scope · scope is permanent</Text>}
      <Text dimColor>Enter:{registerField === 2 ? 'register permanently' : 'next'} · ←:back · Esc:discard</Text>
    </Box>}
    {reconciliation&&<Box flexDirection="column">
      <Text bold color={theme.accent}>Reconcile uncertain usage · all attempts for one message</Text>
      <Text>Manual attestation; neither a provider report nor invoice. No replay.</Text>
      <Text color={reconciliationField===0?theme.accent:theme.text}>Message {reconciliation.targetID} · scope {reconciliation.snapshot?.scope}</Text>
      <Text dimColor>j/k:candidate · n/p:page · r:refresh evidence (at Message field) · page has {reconciliation.page?.candidates?.length??0} candidate(s){reconciliation.page?.truncated?' · more available':''}</Text>
      <Text>Message r{reconciliation.snapshot?.message_revision} · usage r{reconciliation.snapshot?.usage_revision}</Text>
      <Text dimColor>Fingerprint: {reconciliation.snapshot?.journal_fingerprint}</Text>
      <Text>Observed in/out/cache: {reconciliation.snapshot?.observed.input_tokens}/{reconciliation.snapshot?.observed.output_tokens}/{reconciliation.snapshot?.observed.cached_input_tokens} · cost floor ${reconciliation.snapshot?.known_cost_floor_usd}</Text>
      {reconciliation.historical&&<Text color={theme.warning}>Historical receipt snapshot; current accounting is not refreshed.</Text>}
      <Text dimColor>Conservative estimates keep current cost; a cited external record may reduce unpriced estimates to the known floor. Both are manual attestations.</Text>
      <Text>Current contribution in/out/cache: {reconciliation.snapshot?.contribution.input_tokens}/{reconciliation.snapshot?.contribution.output_tokens}/{reconciliation.snapshot?.contribution.cached_input_tokens} · ${reconciliation.snapshot?.contribution.estimated_cost_usd}</Text>
      <Text dimColor>Journal {reconciliation.snapshot?.runs?.length??0}/{reconciliation.snapshot?.run_count} · samples {reconciliation.snapshot?.samples?.length??0}/{reconciliation.snapshot?.sample_count} · prior attestations {reconciliation.snapshot?.previous?.length??0}/{reconciliation.snapshot?.previous_count}{reconciliation.snapshot?.samples_truncated||reconciliation.snapshot?.runs_truncated||reconciliation.snapshot?.previous_truncated?' (evidence list truncated)':''}</Text>
      {reconciliation.snapshot?.previous?.slice(0,2).map(record=><Text key={record.reconciliation_id} dimColor>Prior {record.reconciliation_id} · manual {record.basis} · {record.evidence.slice(0,120)}{record.evidence.length>120?' [excerpt]':''}</Text>)}
      {!reconciliation.snapshot?.can_reconcile&&<Text color={theme.warning}>{reconciliation.snapshot?.reason||'Not eligible'}</Text>}
      {(['input','output','cached','cost','basis','reason','evidence'] as const).map((name,index)=><Text key={name} color={reconciliationField===index+1?theme.accent:theme.text}>{reconciliationField===index+1?'›':' '} {['Total input tokens','Total output tokens','Total cached tokens','Total estimated USD','Basis: e external record / m conservative estimate','Reason','Evidence/source'][index]}: {reconciliation[name]||'—'}</Text>)}
      <Text>I reviewed all attempts and prior effects. These totals are my attestation; later reviews cannot lower them. This does not replay the request. Attestation: {reconciliation.confirmed?'confirmed':'not confirmed'}</Text>
      {reconciliation.needsRefresh&&<Text color={theme.warning}>Nothing committed. Keep entries; return to Message field and press r to refresh, then confirm again.</Text>}
      {reconciliation.frozen?<Text color={theme.warning}>Outcome may be unknown. Enter manually retries the same body/UUID. No automatic retry.</Text>:reconciliation.receipt?<Text color={theme.accent}>Accounting reconciled · {reconciliation.receipt.reconciliation_id}. Request was not requeued. Other uncertainty/limits may remain. Esc:close.</Text>:<Text dimColor>←/→:field · Enter:{reconciliationField===8?'SUBMIT ATTESTATION':'next'} · c:confirm at final field · Esc:close</Text>}
    </Box>}
    {review && <Box flexDirection="column">
      <Text bold>Review blocked request budget</Text>
      <Text>Request {review.messageID} · r{review.expectedRevision} · usage r{review.expectedUsageRevision}</Text>
      <Text>Budget scope: {state?.usage?.scope} {state?.usage?.task_id} · limits apply to this scope</Text>
      <Text color={theme.warning}>Requeue may repeat prior effects. Inspect work before continuing.</Text>
      <Text dimColor>Counters, prerequisites and contract pins remain; backend checks readiness.</Text>
      {(['tokens','cost','note'] as const).map((name,i)=><Text key={name} color={reviewField===i?theme.accent:theme.text}>{reviewField===i?'›':' '} {['Token limit (0 unlimited)','USD limit (0 unlimited)','Review note (required, 2048 UTF-8 bytes)'][i]}: {review[name]||'—'}</Text>)}
      <Text>Confirmation: {review.confirmed?'confirmed':'not confirmed'}</Text>
      {review.frozen?<Text color={theme.warning}>Submitted body locked. Outcome may be unknown; Enter manually retries this exact review ID. No automatic retry.</Text>:<Text dimColor>←/→:field · Enter:{reviewField===3?'SUBMIT REVIEW':'next'} · {reviewField===3?'c:confirm possible repeated effects · ':''}Esc:discard</Text>}
      {review.frozen&&<Text dimColor>Review ID: {review.frozen.review_id} · Esc:close, then refresh before a new review</Text>}
    </Box>}
    {draft && <Box flexDirection="column">
      <Text bold>{draft.reused ? 'Retry draft · original scope locked' : 'New request draft'}</Text>
      {draft.reused && <Text dimColor>Conversation: {draft.conversationID}</Text>}
      {fields.map((name, i) => <Text key={name} color={field === i ? theme.accent : theme.text}>{field === i ? '›' : ' '} {labels[i]}: {draft[name] || '—'}</Text>)}
      {field === fields.length && <Text color={theme.warning}>Review above. Enter sends a new durable request.</Text>}
      <Text dimColor>Contract pins wait for acknowledgement; changed or deleted pins fail the request.</Text>
      {draft.reused && <Text dimColor>Earlier work may have effects. Existing task and budget are reused; blocked work may need review.</Text>}
      <Text dimColor>←/→:field · Enter:{field === fields.length ? 'SEND' : 'next'} · Esc:discard</Text>
    </Box>}
    {!draft && !registration && !review && !reconciliation && <Box flexDirection="column">
      {!detail && <><Text bold>Correspondence ({messages.length}{state?.page.has_more ? '+' : ''})</Text>
      {state?.self && !messages.length && <Text dimColor>No persisted correspondence.</Text>}
      {visible.map((item) => <Box key={item.id} flexDirection="column">
        <Text color={item.id === selected?.id ? theme.accent : theme.text} wrap="truncate-end">{item.id === selected?.id ? '›' : ' '} {stateLabel(item.state)} · {item.task_id || item.kind}</Text>
        <Text dimColor wrap="truncate-end">{names.get(item.from) ?? item.from} → {names.get(item.to) ?? item.to}</Text>
      </Box>)}
      {selected && <Text dimColor>{Math.min(index + 1, messages.length)}/{messages.length} · Enter:inspect · x:cancel · y:retry draft</Text>}</>}
      {detail && <Box flexDirection="column">
        <Text bold>Inspect {detail.message.id}</Text>
        <Text>{stateLabel(detail.message.state)} ({detail.message.state}) · revision {detail.message.revision}</Text>
        <Text color={theme.accent}>{['Overview', 'Usage', 'Activity', 'Dependencies', 'Agreements / artifacts'][inspectTab]} · v:next view · Esc:back</Text>
        {inspectTab === 0 && <><Text dimColor>Task: {detail.message.task_id || 'conversation scope'}</Text>
        <Text dimColor>Conversation: {detail.message.conversation_id}</Text>
        <Text dimColor>Required contracts: {detail.message.required_contracts?.map(pin => `${pin.contract_id}@${pin.revision}`).join(', ') || 'none'}</Text>
        <Text dimColor>Prerequisites: {detail.message.depends_on?.length ?? 0} · {state?.graph?.nodes.find(node => node.message.id === detail.message.id)?.readiness ?? 'unknown'}</Text>
        <Text dimColor>Latest loaded update: {detail.events.at(-1)?.type??'none'} · r{detail.events.at(-1)?.revision??'—'}{detail.has_more?' (more activity available)':''}</Text>
        {detail.events.at(-1)&&<Text dimColor>{payloadText(detail.events.at(-1)!.payload).slice(0,160)}{payloadText(detail.events.at(-1)!.payload).length>160?'… [update excerpt]':''}</Text>}
        <Text>{payloadText(detail.message.payload).slice(0, 240)}{payloadText(detail.message.payload).length > 240 ? '… [excerpt]' : ''}</Text></>}
        {inspectTab===4&&<>
          <Text dimColor>Workspace published agreements · n/p:agreement · j/k:scroll · r:refresh</Text>
          {!state?.contracts&&<Text dimColor>{state?.error?'No agreement snapshot loaded; r retries.':'Loading published agreements…'}</Text>}
          {state?.contracts?.length===0&&<Text dimColor>No published agreements or declared artifacts.</Text>}
          {state?.contracts?.[contractIndex]&&<>
            <Text>Agreement {contractIndex+1}/{state.contracts.length} · {state.contracts[contractIndex]!.id}</Text>
            <Text dimColor>Pinned requirement: {detail.message.required_contracts?.find(pin=>pin.contract_id===state.contracts![contractIndex]!.id)?.revision??'none for this message'}</Text>
            {contractReviewLines(state.contracts[contractIndex]!).slice(contractOffset,contractOffset+8).map((line,index)=><Text key={index}>{line}</Text>)}
            <Text dimColor>Lines {contractOffset+1}–{Math.min(contractOffset+8,contractReviewLines(state.contracts[contractIndex]!).length)} of {contractReviewLines(state.contracts[contractIndex]!).length}. Declarations do not prove files exist or tests pass.</Text>
          </>}
        </>}
        {inspectTab === 1 && usage && <>
          <Text>Usage scope: {usage.scope} {usage.task_id}</Text>
          <Text>Tokens in/out/cache: {usage.input_tokens}/{usage.output_tokens}/{usage.cached_input_tokens}</Text>
          <Text>Estimated USD: ${usage.estimated_cost_usd.toFixed(4)}</Text>
          <Text>Limits: {usage.limits.tokens || 'unlimited'} tokens / ${usage.limits.cost_usd || 'unlimited'}</Text>
          <Text dimColor>Unknown usage/price: {usage.unknown_usage}/{usage.unknown_price}</Text>
          <Text dimColor>Pending/unconfirmed: {usage.pending_executions}/{usage.unconfirmed_executions}</Text>
          {usage.requires_review && <Text color={theme.warning}>Review required: {usage.reason}</Text>}
          <Text dimColor>Budget owner: {names.get(usage.owner_peer_id??'')??usage.owner_peer_id??'unknown'} · usage revision {usage.revision??'unknown'}</Text>
          <Text color={theme.accent}>u:inspect/reconcile uncertain usage evidence (separate from replay)</Text>
          {model.canReview()&&<Text color={theme.accent}>b:review budget and explicitly requeue this blocked request</Text>}
        </>}
        <Text dimColor>{inspectTab === 2 ? `Activity: ${detail.events.length} loaded · j/k:scroll` : ''}</Text>
        {inspectTab === 2 && detail.events.slice(activityOffset, activityOffset + 4).map((event) => <Text key={event.revision} wrap="truncate-end">r{event.revision} {event.type}: {payloadText(event.payload)}</Text>)}
        {inspectTab === 2 && detail.events.length > 4 && <Text dimColor>Showing {activityOffset + 1}–{Math.min(activityOffset + 4, detail.events.length)} of {detail.events.length}.</Text>}
        {inspectTab === 2 && detail.has_more && <Text dimColor>a:load next activity page</Text>}
        {inspectTab === 3 && state?.graph && <>
          <Text>Request dependency graph · j/k:scroll</Text>
          <Text dimColor>Root: {state.graph.root_id}</Text>
          {state.graph.nodes.slice(graphOffset, graphOffset + 3).map(node => <Box key={node.message.id} flexDirection="column">
            <Text>{node.message.id} · {node.readiness} ({node.message.state})</Text>
            <Text dimColor>Prerequisites: {node.message.depends_on?.join(', ') || 'none'}</Text>
            <Text dimColor>Contract pins: {node.message.required_contracts?.map(pin => `${pin.contract_id}@${pin.revision}`).join(', ') || 'none'}</Text>
            {(node.contract_blockers ?? []).map((blocker, index) => <Text key={`${blocker.contract_id}:${index}`} color={theme.warning}>Agreement {blocker.contract_id}: {contractBlockerLabel(blocker)}</Text>)}
            {node.blockers.map(blocker => <Text key={blocker.id} color={theme.warning}>Waiting on {blocker.id}: {blocker.state} · {blocker.reason}</Text>)}
          </Box>)}
          <Text dimColor>Showing {state.graph.nodes.length ? graphOffset + 1 : 0}–{Math.min(graphOffset + 3, state.graph.nodes.length)} of {state.graph.nodes.length} loaded nodes.</Text>
          {state.graph.truncated && <Text color={theme.warning}>Graph truncated at 100 nodes; additional prerequisites are omitted.</Text>}
          {state.graph.hidden_dependencies && <Text color={theme.warning}>Some prerequisites are hidden; access is limited to your participation.</Text>}
          <Text dimColor>Readiness combines prerequisites and contract agreement; budget/runtime gates still apply. Agreement and completion are not test proof. r:refresh</Text>
        </>}
      <Text dimColor>x:cancel · y:retry draft</Text></Box>}
      <Text dimColor>n:new request · r:refresh · o:older</Text>
      <Text dimColor>j/k:select · Tab:sidebar tab</Text>
      <Text dimColor>Cancel requests stop; prior effects remain.</Text>
    </Box>}
  </Box>;
}
