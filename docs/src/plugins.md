# Plugins

Plugins extend Overkill with custom tools, slash commands, and lifecycle hooks. They run as subprocesses communicating over JSON-RPC 2.0 on stdio.

## Quick example

```go
p := sdk.New(sdk.Manifest{Name: "hello", Version: "0.1.0"})
p.OnTool("greet", func(ctx context.Context, args json.RawMessage) (any, error) {
    return map[string]string{"text": "hi!"}, nil
})
p.Run()
```

## Install

```sh
overkill plugin install https://github.com/you/your-overkill-plugin
```

Plugins live in `~/.overkill/plugins/<name>/`. A Go SDK is available at `examples/plugins/sdk-go/`.

## Reference plugins

- `examples/plugins/notes/` — adds a `notes` tool backed by a local file
- `examples/plugins/git-stats/` — adds a `git_stats` tool
