import { useEffect, useRef } from "react";
import type { BackendClient } from "../backend/client.ts";
import type { ClarifyRequest } from "./use-dialogs.ts";

interface ClarifyPollResult {
  session_id: string;
  question: string;
  choices: string[];
}

export function useClarifyPoll(
  backend: BackendClient,
  showClarify: (req: ClarifyRequest, cb: (answer: string, index: number) => void) => void,
  clarifyOpen: boolean,
) {
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const sessionRef = useRef<string | null>(null);

  useEffect(() => {
    // Don't poll while a dialog is already open.
    if (clarifyOpen) {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
      return;
    }

    const poll = () => {
      backend
        .call<ClarifyPollResult | null>("clarify.poll")
        .then((result) => {
          if (!result || !result.question) return;

          sessionRef.current = result.session_id;

          showClarify(
            {
              question: result.question,
              choices: result.choices ?? [],
            },
            (answer: string, index: number) => {
              // Send the answer back to the agent.
              const sid = sessionRef.current;
              if (sid) {
                backend
                  .call("agent.answer", {
                    session_id: sid,
                    text: answer,
                    index,
                  })
                  .catch((err: unknown) => {
                    console.error("agent.answer (clarify) failed:", err);
                  });
              }
              sessionRef.current = null;
            },
          );
        })
        .catch((err: unknown) => {
          console.error("clarify poll failed:", err);
        });
    };

    intervalRef.current = setInterval(poll, 500);

    return () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
      }
    };
  }, [backend, showClarify, clarifyOpen]);
}
