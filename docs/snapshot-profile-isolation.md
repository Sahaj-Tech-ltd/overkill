# Snapshot profile isolation

Manual `snapshot export`, `snapshot list`, and the daemon’s existing daily
snapshot tick resolve storage through `config.ConfigDir()`. `OVERKILL_HOME`
therefore selects the snapshot directory together with the active configuration.
An invalid selected profile returns an error; it never falls back to the default
home directory. Scheduled failures are logged.

Exports use unique private files (0600) beneath that profile’s `snapshots`
directory. Repeated exports in one second preserve both versions. Session IDs
containing path separators or NUL, empty IDs, and dot/dot-dot IDs are rejected.
Failed writes remove the incomplete file. Listing propagates entry-stat errors.
This is a session JSON export, not database backup, automatic retention,
archive restoration, or disaster recovery. The daily tick retains its existing
50-session bound.

Executed regression evidence on macOS arm64:

- `.cache/snapshot-profile-before.log`: the original implementation wrote the
  default home’s directory despite a different selected profile.
- `.cache/snapshot-profile-after.log`: three focused race tests pass, covering
  profile selection, manual/shared and scheduled writing, JSON payloads, unique
  names, private modes, unsafe IDs, and invalid-profile failure without fallback.

Command: `go test -race ./cmd/overkill -run '^TestSnapshot' -count=1`.
