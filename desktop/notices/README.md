# Dependency notices: evidence snapshot

This directory contains verbatim license/notice materials and explicitly labeled source-header evidence collected for the candidate macOS desktop resources. `manifest.json` gives each retained file's origin and SHA-256. The generated manifest and this README are not third-party license texts.

Two lib/pq source notices retain a `.go.txt` suffix so repository Go tooling treats them as inert text. Their bytes, hashes and original module-source provenance are unchanged.

Read `docs/desktop-dependencies.md` and `docs/desktop-dependencies.json` for component versions, binary hashes, source artifacts and outstanding source/license evidence. **Copying this directory alone does not fulfill every source or redistribution obligation.** The inventory deliberately records `distribution_ready: false`.

- `application/`: the repository's MIT and Apache texts.
- `go/`: exact versioned module-cache notices, verified byte-for-byte against the matching module ZIPs. Names retain module identity/version; nested third-party notices remain nested.
- `go-runtime/`: the exact Go 1.26.10 source tree's license files, conservatively including toolchain notices beyond runtime-only code.
- `electron/`: the actual Electron 45 LICENSE and complete Chromium credits, matching Node license, plus license/build-reference material from the pinned Chromium FFmpeg source.
- `native/` and `native-source/`: notices from matching identified upstream releases/source archives. An upstream release match is not proof that a vendor binary has no patches.
- `native-vendor/`: the recovered PostgreSQL server and third-party license files from EDB's 17.11-1 macOS ZIP; selected dylibs match prepared binaries exactly. The vendor text's grouping/attribution gaps remain recorded.
- `native-extensions/`: official candidate notices for system_stats and plugin_debugger. Exact vendor build commits are not established. The debugger header excerpt is labeled; its full Artistic 2.0 text is from the SPDX canonical catalog after the linked OSI page returned HTTP 403.
- `source-evidence/`: exact upstream header/credit files establishing version constants or license selection. These are evidence excerpts/files, not a complete corresponding-source release.

No upstream code was executed during collection. Matching source archives are retained under the ignored `research/desktop-dependencies/sources/` directory; they are not automatically included in an app/DMG or published to recipients. Any final package must include the necessary notices and the chosen recipient-facing source/replacement instructions, and its hashes must be checked against a new final inventory.
