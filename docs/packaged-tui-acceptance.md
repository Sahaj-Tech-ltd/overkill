# Installed terminal UI acceptance

`scripts/verify-packaged-tui.py --binary /absolute/path/to/bin/overkill`
launches the real Go `tui` command and its compiled Ink entry point inside a
pseudo-terminal. Set `DATABASE_URL` to a disposable acceptance database (or an
owned, isolated schema through the connection's `search_path`). Alternatively,
unset `DATABASE_URL` and pass `--local-database-bin /installed/postgres/bin`
to exercise the explicit owned local database mode from an empty profile. The
two modes are mutually exclusive. The local mode requires installed binaries,
checks that the initialized cluster persists after exit, and checks that its
postmaster has stopped before removing the disposable profile. Failure cleanup
can stop only this exact fixture-owned cluster. The command
initializes tables there; do not use a production database.

The check creates a fresh profile and an unrelated workspace, strips inherited
application/provider configuration, and restricts `PATH` to the binary's
installation directory. The installation must contain Node and the compiled
frontend in a layout recognized by the CLI. No source frontend override or
system Node fallback is allowed. It waits for the connected status hint, opens
the command palette with Ctrl+K, filters keyboard help, opens the actual help
dialog, and quits with Ctrl+C. Success requires exit code zero and no surviving
process in the owned process group. Failed runs forcibly clean up that group.
It does not invoke a model provider.

`scripts/verify-docker.sh` executes this check as the image's non-root user after
building the image and bringing up its uniquely named disposable PostgreSQL
stack. The image ships the acceptance script alongside the bundled frontend.
The existing image imports, configuration validation and authenticated API
checks still run.

The native staging acceptance uses a copied Go binary, a copied Node executable,
compiled `tui/dist`, and a fresh `npm ci --omit=dev --ignore-scripts` dependency
installation under `share/overkill/tui`. It contains no `src`, `tsx`, or
TypeScript runtime. This verifies that installed layout on the current native
platform. It does not establish Docker, Windows, Linux, release archive,
installer, signing, or DMG acceptance.

This process check exposed a real command-palette crash when filtering results:
the highlight helper called `useTheme` once per highlighted row, changing React
hook counts. The helper now receives the accent color from the component's
unconditional hook. A compiled Ink regression filters across different result
counts and executes a selected action.


2026-10-22 reinspection: `.cache/installer-audit-packaged-tui.log` and
`.cache/installer-audit-packaged-tui.json` record both external isolated-schema
and fresh owned local-database modes against the current native arm64 binary.
The staging copy uses fresh offline production npm dependencies, current built
frontend and bundled Node. Both actual PTY runs pass. The owned database and
staging/profile directories are removed; no provider calls occur. This adds
native first-launch bootstrap proof, while Docker and other native platforms
remain separate gates.
