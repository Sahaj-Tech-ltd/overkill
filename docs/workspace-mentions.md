# Workspace-bound file mentions

Automatic `@path` input context previously resolved against the process cwd
and used an unbounded whole-file read before truncating. Session tools and
instructions already carried the selected chat directory, so a non-streaming
API turn could inject the wrong workspace or an external absolute file. Streaming
turns skipped the preload altogether.

Agent construction now binds an immutable workspace from the actual CLI cwd
or canonical persisted API session. An absent root disables automatic preload;
it never falls back to a shared process directory. Run and Stream use the same
bounded loader. Paths resolve through `os.Root` with symlink containment,
regular-file checks and nonblocking Unix open. At most10 unique files among
128 examined mention tokens contribute16 KiB prefixes, read with limit-plus-one
truncation detection. Overlong paths fail without filesystem access. The shared
child runtime continues to disable automatic mentions and use contained tools.
This is text file context, not a generic attachment/image upload protocol or an
OS sandbox for trusted hooks/plugins.

The actual legacy-loading reproduction is `.cache/workspace-mentions-legacy-before.log`.
It overlays the immutable HEAD `mentions.go` and its legacy Agent loader call
into the current API fixture; it is a consumer-path legacy-source reproduction,
not a complete historical application binary. The provider request contains
both harmless ambient and external proof markers and misses the selected marker.
The research lane's earlier `.cache/frontend-mentions-before.log` ran after edits
and is explicitly an invalid BEFORE attempt, not a historical leak receipt.

After repair, `.cache/workspace-mentions-after.log` executes actual API assembly
for two independent workspaces through both Run and Stream. The HTTP provider
receives each selected marker, no ambient/external marker, and a correctly
truncated prefix from a1 GiB sparse file. Outside symlink reads are denied.
Existing mention deduplication/count/unreadable-file tests pass.
`.cache/workspace-mentions-cli.log` checks a separate built CLI loading real
configuration sees its own referenced file while configured readonly denies
filesystem writes and is inherited by actual children.
