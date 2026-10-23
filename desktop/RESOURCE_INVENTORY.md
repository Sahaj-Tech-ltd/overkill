# Native resource inspection

Measured on macOS arm64, 2026-10-21. This records local build evidence; app/DMG installation and redistribution review remain pending.

`node scripts/prepare-desktop.mjs` prepares the pinned PostgreSQL 17.11 archive and current Go backend. The archive URL and expected SHA-256 live in that script; the generated `resource-manifest.json` records the backend checksum and explicit omissions.

Run the read-only audit after preparation:

```sh
python3 scripts/audit-desktop-resources.py .cache/desktop-resources > .cache/desktop-resource-inventory.json
```

The audit hashes every regular file, records symlink destinations, rejects escaping/broken links, extracts Mach-O architectures/platforms/load paths and verifies non-system loader dependencies resolve inside the prepared tree. System dyld shared-cache libraries are recognized by their system paths. It does not establish runtime behavior, license compliance or signing validity.

Latest prepared resource tree: **986 regular files, 118 Mach-O binaries, zero unresolved non-system dependencies**, with arm64 present in every executable/library. Its Mach-O minimum platform is macOS 12.0. The pinned Electron 45.0.0 executable and framework require macOS 13.0, so the complete desktop target has a **macOS 13 minimum**, subject to installed-app verification.

The original vendor bundle also advertises optional Perl/Python/Tcl/XML extensions whose libraries reference absent `/Library/edb/languagepack/v5/` runtimes or missing libxslt. The preparer omits those ten modules and all twelve corresponding extension names' SQL/control files together, and records each omitted file. Overkill does not use these extensions. Core PostgreSQL, native private-cluster setup, persistence, crash recovery and chat acceptance remain tested against the prepared tree.

Redistribution work is unfinished. The vendor JAR contains no dependency license texts. Bundled libraries include PostgreSQL/libpq, OpenSSL, ICU, zstd, LZ4, libxml2, zlib, Kerberos, libedit, libiconv, libintl and libuuid; component versions, complete notices and applicable source obligations need a pinned inventory. Electron includes `LICENSE` and `LICENSES.chromium.html`; the Go binary's embedded module metadata supplies its dependency version list, which also needs corresponding notices. The resource manifest deliberately retains `distributionReady: false`.
