# User file hooks

Overkill loads executable hooks from `<configuration directory>/hooks/<event>/` when constructing an agent. The normal configuration directory is `~/.overkill`; desktop profiles use their own configuration directory. Scripts in a workspace are never discovered automatically. Each agent has its own registry. Restart/recreate the agent after adding or removing scripts; this is not live hook reload.

Events are `before_tool_call`, `after_tool_call`, `after_turn`, `on_session_start`, `on_session_end`, `on_error`, `before_compaction`, and `after_compaction`. An admitted first Run/Stream fires session start once, and Shutdown fires session end once with a separate context bounded to five seconds. Forced process termination cannot run cleanup hooks.

Scripts receive a JSON event on standard input, including the dispatch `point`, session ID, and tool name/input/output when applicable. Before/after tool events are emitted by the executing agent rather than synthesized by the UI. Hooks are observational: failures are logged and do not authorize or block tool execution. Each script gets at most five seconds, bounded further by the caller's context; process-tree cancellation applies.

On Unix, hooks must be regular executable files owned by the current user, with no group/world write permission. Hook directories must also be owned by that user and not writable by a group or everyone. Linked files/directories are rejected; trust is checked again before execution. Windows accepts `.exe`/`.ps1` only, with current-user ownership and restricted DACLs; PowerShell preserves machine execution policy. Native Windows execution is not certified by the macOS tests.

User hooks run with the application's OS privileges and can observe tool inputs/outputs. Install only scripts you trust into this owner-controlled directory. This mechanism does not make scripts a sandbox or permission policy.

Acceptance executes real scripts through the production shared API/ACP session factory and the built standalone CLI. It verifies before/after ordering, actual `fs`/`ask_user` tool calls, session identity, and exclusion of executable workspace scripts. Separate tests verify lifecycle ownership and unsafe/changed script permissions.
