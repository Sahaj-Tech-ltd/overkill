import { useEffect, useRef } from "react";
import type { BackendClient } from "../backend/client.ts";
import type { ClarifyRequest } from "./use-dialogs.ts";
import { log } from "../logger.ts";

interface ClarifyPollResult {
  session_id: string;
  question_id: string;
  question: string;
  choices: string[];
}

export function useClarifyPoll(
  backend: BackendClient,
  showClarify: (
    req: ClarifyRequest,
    cb: (answer: string, index: number, cancel?: boolean) => void,
  ) => void,
  clarifyOpen: boolean,
) {
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);


  useEffect(() => {
    // Don't poll while a dialog is already open.
    if (clarifyOpen) {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
      return;
    }

    let active = true, inFlight = false;
    const poll = () => {
      if (inFlight) return;
      inFlight = true;
      backend
        .call<ClarifyPollResult | null>("clarify.poll")
        .then((result) => {
          if (!active || !result || !result.question || !result.question_id) return;

          showClarify(
            {
              question: result.question,
              choices: result.choices ?? [],
            },
            (answer: string, index: number, cancel = false) => {
              backend.call("agent.answer", {
                session_id: result.session_id,
                question_id: result.question_id,
                text: answer, index, cancel,
              }).catch((err: unknown) => log.error("agent.answer (clarify) failed:", err));

            },
          );
        })
        .catch((err: unknown) => {
          log.error("clarify poll failed:", err);
        }).finally(() => { inFlight = false; });
    };

    intervalRef.current = setInterval(poll, 500);

    return () => {
      active = false;
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
      }
    };
  }, [backend, showClarify, clarifyOpen]);
}
