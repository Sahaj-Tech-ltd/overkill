# Extension inventories and controls

The unified registry now reports actual skill, hook, plugin and MCP inventories through CLI/API entrypoints. `can_toggle` and `unsupported_reason` describe supported operations; configured/discovered is distinct from running.

| Family | Inventory | Controls and limits |
| --- | --- | --- |
| Skills | Shared canonical skill registry | Persistent enable/disable in production startup; subsequent real provider prompts reflect the setting. |
| File hooks | Trusted configured scripts; executing session hook registry | API toggles register/unregister supported file hooks in the selected session. Application-owned hooks cannot be toggled. Session toggles reset when that runtime is rebuilt. Global inventory requires selecting a session for executing controls. |
| Plugins | Real discovered metadata and configured policy | Application plugin process execution remains unwired. Discovery does not launch an executable. Runtime toggles return an explicit unsupported error. |
| MCP | Configured servers; API live manager connection/tool counts | Per-server runtime toggles are unsupported. Standalone CLI inventory does not imply that it started a connection. Edit configuration and restart where supported. |

Completion hooks now run exactly once after successful normal or streaming turns, inside the admitted turn with a five-second shared deadline. Turn/session cancellation terminates their processes, and shutdown joins a running completion hook before session-end work. Canceled/blocked turns do not acquire a successful-turn hook merely because a provider returned data.

Actual controlled CLI/API acceptance verifies persistent skill changes, trusted script effects with hook disable/re-enable, live MCP status, plugin nonexecution, secret omission and unsupported controls. Additional lifecycle fixtures verify nonstream/stream exactly-once completion, cancellation and shutdown cleanup. No real external MCP account or plugin process execution is claimed.
