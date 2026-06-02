# Install

## One-liner (Linux, macOS, WSL)

```sh
curl -fsSL https://raw.githubusercontent.com/Sahaj-Tech-ltd/overkill/main/install.sh | sh
```

The installer detects your platform, prefers `go install` when Go is on `PATH`, otherwise downloads a pre-built binary, drops it in `~/.local/bin/overkill`, and bootstraps `~/.overkill/`.

## Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/Sahaj-Tech-ltd/overkill/main/install.ps1 | iex
```

Installs to `%LOCALAPPDATA%\overkill\bin\overkill.exe` and adds it to your user `PATH`.

## With Go

```sh
go install github.com/Sahaj-Tech-ltd/overkill/cmd/overkill@latest
```

## From source

```sh
git clone https://github.com/Sahaj-Tech-ltd/overkill.git
cd overkill
make install-all   # builds and copies to ~/go/bin/overkill
```

## Runtime dependencies

Only `git` is required at runtime. Optional, auto-detected:

- `gopls`, `typescript-language-server`, `pyright`, `rust-analyzer` — LSP tools
- Any MCP server you wire up
- `gh` — GitHub Gist sharing

## Supported platforms

| Platform | Arch | Installer |
|---|---|---|
| Linux | x86_64, arm64 | `install.sh` |
| macOS | Intel, Apple Silicon | `install.sh` |
| Windows | x86_64 | `install.ps1` |
| WSL2 | x86_64, arm64 | `install.sh` |
