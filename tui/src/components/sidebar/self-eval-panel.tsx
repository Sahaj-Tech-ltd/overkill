import React from "react";
import { Box, Text } from "ink";
import type { BackendClient } from "../../backend/client.ts";
import type { SelfEvalStatus } from "../../backend/types.ts";
import { useSessionStatus } from "../../hooks/use-session-status.ts";
import { useTheme } from "../../hooks/use-theme.ts";

export function SelfEvalPanel({backend, sessionId}: {backend: BackendClient; sessionId: string | null}): React.JSX.Element {
  const {theme} = useTheme();
  const {value, error, loading} = useSessionStatus<SelfEvalStatus>(backend, sessionId, "self.eval.status");
  const plan=value?.plan_execution;
  const firstUnfinished=plan?.phases.findIndex(phase=>!['done','skipped'].includes(phase.status))??-1;
  const firstVisible=firstUnfinished<0?Math.max(0,(plan?.phases.length??0)-4):Math.max(0,firstUnfinished-1);
  const phaseLabels:Record<string,string>={done:'policy accepted',pending:'not started',running:'working',deferred:'needs review',failed:'failed',unconfirmed:'unconfirmed',skipped:'skipped'};
  return <Box flexDirection="column" paddingX={1}>
    <Text bold color={theme.accent}>Evaluation &amp; Checks</Text>
    {loading && <Text>Loading...</Text>}
    {error && <Text color={theme.error}>{error}</Text>}
    {!sessionId && <Text dimColor>Select a session.</Text>}
    {value && <>
      <Text>State: {value.status} · {value.phase}</Text>
      {value.iterative_evaluation ? <>
        <Text>Last loop: {value.iterative_evaluation.status} · {value.iterative_evaluation.phase}</Text>
        <Text>Iteration: {value.iterative_evaluation.iteration}/{value.iterative_evaluation.max_iterations}</Text>
      </> : <Text dimColor>Iterative evaluation not recorded for this chat.</Text>}
      {plan && <>
        <Text bold>{plan.current ? 'Current plan' : 'Previous plan'}: {plan.title}</Text>
        <Text>Execution: {plan.status} · {plan.phases.filter(phase=>phase.status==='done').length}/{plan.phases.length} phases accepted</Text>
        {plan.phases.slice(firstVisible,firstVisible+4).map((phase,index)=><Box key={index} flexDirection="column"><Text wrap="truncate-end">{phase.title}: {phaseLabels[phase.status]??'unknown'}</Text>{phase.note&&<Text dimColor wrap="truncate-end">{phase.note}</Text>}</Box>)}
      </>}
      <Text>Passed: {value.verification.passed} · Failed: {value.verification.failed}</Text>
      <Text>Skipped: {value.verification.skipped} · Canceled: {value.verification.canceled}</Text>
      <Text dimColor>Confidence acceptance is not a test pass.</Text>
    </>}
  </Box>;
}
