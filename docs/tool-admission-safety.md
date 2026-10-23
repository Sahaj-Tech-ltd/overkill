# Reader admission and configured approvals

The research pass reproduced two separate tool-admission defects through real
agent execution, with effects confined to temporary workspaces.

The old Reader-mode gate guessed shell effects from a leading-verb regular
expression. `printf reviewed > reader-mode-proof.txt` passed that gate and wrote
a file through the default-scanned ShellTool. Reader mode now admits explicitly
classified observations: native file reads/glob/stat, search/LSP queries, question
interaction and selected durable status/read operations. Shell/PTY, Git execution,
unknown adapters and mutating or malformed actions require writer authority.
The unified `fs edit` action is included. There is no command-text heuristic
claiming an arbitrary interpreter, compound command or project-configured Git
process is read-only.

Executed receipts: `.cache/reader-mode-research-before.log` records the actual
pre-fix Agent→Shell failure. `.cache/reader-mode-safety-after.log` passes the
same path plus redirection, compound/interpreter/environment cases, denied edit,
preserved native read and a real writer-mode shell effect. The writer default
is unchanged. The accepted `security.autonomy_level = "readonly"` configuration
now sets an independent admission ceiling through the existing CLI/API runtime.
Installing or switching an optional writer gate cannot override that ceiling.
The shared child factory inherits configured readonly, and denied children
cannot manufacture verified file-work receipts. A trusted configuration change
back to supervised restores ordinary writes; this is not an in-app mode picker. This repairs the component rather than claiming a new end-to-end
planning mode or an OS sandbox for trusted plugins/hooks.

The approval risk classifier separately recognized legacy file-write names but
missed the actual unified `fs` action. With a denial callback explicitly installed,
a real Agent.Run wrote a file and never invoked that callback. The classifier
now recognizes those writes; opaque unclassified operations no longer silently
inherit low risk. `.cache/fs-approval-before.log` proves zero callback invocations
and the real unwanted write. `.cache/fs-approval-after.log` proves the callback
is reached and denial preserves the workspace.

Approval callbacks are still not installed by the production CLI/API assembly.
No permission configuration, automatic approval tier, deny precedence, interactive
permission UI or approval persistence is inferred from this component repair.
`SetAutoMode` controls autonomous planning/evaluation; it is not a configured
resource-permission engine. Human clarification and tool permission are distinct
contracts. These limits are retained in the harness comparisons.


Configured readonly had previously been accepted but ignored: the actual agent
fixture created its proof file (`.cache/configured-readonly-before.log`).
After repair, `.cache/configured-readonly-after.log` proves denied filesystem
and shell writes, preserved reads and deliberate writer restoration.
`.cache/configured-readonly-api.log` runs the actual API assembly through Run
and Stream against an HTTP model fixture, checking denial reaches the provider
and supervised writes still work. `.cache/configured-readonly-cli.log` runs a
separate built CLI loading real TOML, and its actual configured child factory;
neither creates the attempted file. Trusted hooks/plugins and direct host
application effects remain outside tool admission; no OS isolation is claimed.
