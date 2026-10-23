# MCP declarations and discovery

`enabled` is optional: omitted preserves the legacy default on; explicit `enabled = false` launches no subprocess and exposes no tools.

`NewManager` applies each configured server's declaration before forwarding a tool call. An empty declaration retains legacy trusted behavior: all currently advertised tools are allowed, without a configured byte/path limit. `trusted = false` with no tool allowlist denies every tool. A nonempty `allowed_tools` list always restricts calls, including when `trusted = true`. `max_bytes_per_call` limits the original encoded arguments and any rewritten path arguments. Negative byte limits and invalid path declarations prevent manager startup.

These are **tool-call controls, not an OS sandbox**. The MCP executable still runs with the application's host permissions and can act independently of tool calls. Only launch binaries the operator authorizes. `trusted` controls the default tool allowlist; it does not install filesystem, network or process isolation.

## Explicit local path arguments

For a local stdio server whose API accepts filesystem paths, configure an existing absolute workspace and existing absolute allowed roots, then declare the complete path argument contract for each permitted tool:

```toml
[[mcp.servers]]
name = "workspace-files"
command = "/absolute/path/to/operator-approved-mcp-server"
trusted = false
allowed_tools = ["read_file", "read_many", "health"]
max_bytes_per_call = 65536
path_workspace = "/absolute/path/to/workspace"
allowed_path_prefixes = ["/absolute/path/to/workspace/project"]

[mcp.servers.path_argument_bindings]
read_file = ["/path"]
read_many = ["/paths"]
health = []
```

The child process starts in `path_workspace`. Relative commands containing a path separator (such as `./server`) are resolved against that workspace before executable lookup, rather than the caller's directory. Relative bound paths resolve against that directory. A binding is an RFC6901 JSON pointer selecting a string or string array. Nested objects and explicit numeric array indexes are supported; `~1` decodes `/` and `~0` decodes `~`. Prefer selecting a complete string array rather than a single index when every entry is a path. Wildcards, root pointers, URI interpretation and arbitrary JSON-key guessing are not supported.

An explicit empty list means the operator declares that the tool has **no path inputs**. A missing entry denies calls to that tool under a path policy. The current discovered tool must provide an object input schema; nonempty pointers must resolve through supported object/array schema nodes to string or string-array fields. Missing fields, incompatible or unsupported schema shapes, non-string values, dangling symlinks and uninspectable parents are rejected before transmission.

Existing symlinks resolve before containment checks. For a new file/directory path, the closest existing ancestor is resolved and the remaining suffix is appended. Every resulting path must stay beneath a canonical allowed root, using path-component containment. Bound values are rewritten to those canonical absolute paths before forwarding, preventing a different relative working directory interpretation. Out-of-root and symlink-escape calls do not reach the server.

The operator must bind **every** filesystem input in the tool's actual contract. These local path semantics are unsuitable for a server that interprets values as remote-host paths or non-filesystem identifiers. This check does not attest the server's implementation, constrain unbound side effects, lock symlinks against later changes, or eliminate filesystem TOCTOU races. Use a separately restricted process/OS environment when those guarantees are required.

## Dynamic catalogs and ownership

API/ACP session service construction prepares a manager without launching subprocesses. `StartBackground` starts it under the service lifecycle. Initialization has a ten-second deadline. Each connected server refreshes its tool catalog every second with a two-second discovery deadline. Discovery failure disconnects and restarts the server using bounded backoff. No accepted tool call is automatically replayed after a disconnect.

The API reconciles adapters into its shared registry and existing session registries every 200 ms. Later sessions receive the current adapters during assembly. Adapters expose the current description and input schema to the provider. A stale, disconnected, removed or denied adapter is omitted from tool lists and refuses execution. Native tools keep their registered instance when names collide; MCP discovery never replaces them. Catalogs are limited to 256 tools per server, bounded names/descriptions/schema sizes, and at most 64 configured servers.

Shutdown cancels initialization, discovery and owned subprocess trees, then joins supervisors and reconciliation. A stopped manager cannot restart. The direct CLI entry points that do not construct the shared API/ACP service do not independently start an MCP manager.

Acceptance tests launch only the repository's controlled local stdio fixture. They check configured allowlists, byte limits, canonical allowed paths and denied effects, delayed discovery, reconnect/stale denial, real provider tool definitions, native collisions and shutdown during initialization. They do not run third-party MCP services.
