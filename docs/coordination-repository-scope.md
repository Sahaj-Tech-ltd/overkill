# Repository scope for explicitly registered local agents

Local agent identities normally belong to one canonical workspace directory. A
chat in a linked Git worktree therefore has a different peer inventory by
default. The application may explicitly opt a new identity into repository
scope using `RegisterRepositorySessionPeer(ctx, sessionID, name, role)`. The
ordinary `RegisterSessionPeer` method retains its workspace behavior.

Repository scope groups primary and linked worktrees by the SHA-256 of their
canonical, absolute Git common directory (`repository:<hash>`). Every member
must explicitly opt in. A default identity in the same folder, an unregistered
chat, and a separately cloned repository retain separate inventories and
correspondence. This also works for repositories without a first commit. Git
must be installed locally; inspection never downloads it.

Registration requires a persisted session whose folder resolves to the exact
root of a non-bare worktree. A folder alias is canonicalized. Subdirectories,
nonrepositories, bare repositories, symlink `.git` entries and symlink Git
identity directories are rejected. Local `git rev-parse` calls inspect the
worktree root, common directory, HEAD and branch. Calls use a five-second total
inspection deadline, bounded output and process-tree cancellation. Inherited
`GIT_*` variables and global/system Git configuration cannot redirect the
inspection. These calls neither allocate worktrees nor execute Git hooks,
network operations or shell commands.

The peer, session binding and repository identity snapshot commit together.
Concurrent identical registration is idempotent. Names are unique within their
scope; an existing name or session cannot be transferred. Retrying registration
with a different scope conflicts, including attempts to upgrade an existing
workspace identity. The generic external peer registration operation rejects
the reserved `repository:` namespace.

The snapshot records the canonical worktree and common paths plus their
filesystem directory identities (device/inode on Unix; volume/file ID on
Windows). Resolution, inventory, peer messaging, claiming, completion, agreement
operations and filesystem ownership guards revalidate that snapshot. The dispatcher
reports an invalid peer without blocking unrelated projects. Read APIs
also reject a stale cached repository peer ID. Changing the session folder,
moving or replacing the Git identity, or replacing either directory fails
closed. A newly registered peer cannot inherit an earlier project's authority
by creating a different repository at the same common-directory pathname.
Directory identity checks persist across process restarts. They do not create a
filesystem sandbox: external filesystem mutation can still race a check, and
commands already running cannot be retroactively undone.

`LocalPeersForSession` returns repository members across their individual
worktree roots only for a validated repository-bound chat. Each result includes
`scope: "repository"` and `repository: {scope, root, common_dir, head?, branch?}`.
Default inventory results include `scope: "workspace"`. The trusted
`RepositoryInfoForSession` inspector returns repository metadata for an opted-in
chat; unregistered and workspace-scoped chats return not found. HEAD and branch
are current observations and may change without altering repository membership.
They are not task completion, exclusive worktree ownership or acceptance proof.

Existing durable requests, dependency gates, pinned agreement revisions,
acknowledgements and ownership reservations use the shared project. A recipient
still executes in its own immutable session/worktree folder. Relative ownership
paths reserve logical repository paths across worktrees. Physical disjointness
rules for opaque command tools remain the existing guard policy; this does not
merge branches, allocate worktrees, rebase changes or reconcile conflicting
files. Those operations require separate integration and verification.

Real Git/PostgreSQL tests in `internal/coordination/repository_scope_test.go`
exercise linked worktree exchange and dependency/agreement gates, restart,
legacy and unrelated-repository isolation, aliases, invalid roots, environment
overrides, concurrent scope selection and registration, named agent collisions,
retargeted sessions and same-path replacement fencing. Windows directory ID
support can be cross-compiled; native Windows acceptance remains a separate gate.

The authenticated `coordination.register` application RPC accepts the optional
boolean `repository_scope`. The server derives repository authority from the
persisted session; callers cannot supply a project ID, repository path or sender.
Desktop registration exposes an unchecked repository-worktrees checkbox. It
resets when switching chats. The terminal Agents panel defaults to checkout
scope and allows `s` to select repository scope on the registration review page.
Both interfaces show the resulting fixed scope. Existing registered chats must
keep their original scope; use a new chat to choose another scope.

This registration option does not connect worktree allocation to a task's
lifetime. The separately hardened worktree manager records allocation ownership,
serializes independent managers, preserves pre-existing branches and refuses
cleanup of dirty or ignored files. See [worktree lifecycle](worktree-lifecycle.md).
Branch merging, automatic allocation per message and full review/resolution UI
remain open integration work.

Saved history in the PostgreSQL-backed app can be opened while a recipient is
working. The `session.messages` RPC uses the store's optional `HistoryReader`
capability and labels its result `snapshot: "committed"`. One ordinary database
read returns independently decoded message data, so an in-flight row update is
neither exposed nor awaited. After commit, a refresh reads the new transcript.
The desktop Refresh chats action also refreshes the selected transcript. This
is a saved snapshot, not live token streaming from the recipient. Other store
implementations retain exclusive history admission unless they implement the
same safe snapshot capability. Turn execution and mutations still serialize.
