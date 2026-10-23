# Development DMG acceptance — 2026-10-22

The packaging pipeline now creates a real macOS arm64 application and compressed DMG. These are **local development artifacts**, ad-hoc signed, without Developer ID signing/notarization. Dependency/source requirements in [the inventory](../docs/desktop-dependencies.md) remain open; `distributionReady` stays false.

## Earlier executed acceptance

The development image under `.cache/distribution/overkill-0.1.0-dev-arm64-kk8A6c/` passed actual installed acceptance on macOS **26.5.2 arm64**. SHA-256: `46b2cf13a3b2480be63c9c50587779b3cc68d05f58558f93b1650ab60107cf1a`. Its `installed-acceptance.json` and `artifact.json` bind the result to that exact image.

The test verified image integrity and a read-only mount, copied the app to an installation path containing spaces, verified its signature, and launched its packaged executable from outside the checkout. Installed apps ignored development resource/data overrides. An explicit temporary profile isolated application data. A controlled local provider drove encrypted setup, a real workspace file edit, stream cancellation, registered frontend/backend agent exchange, and persisted chat/correspondence. Same-version replacement from the mounted image, relaunch and an owned-database crash/reconnect preserved the committed data. It did not test paid providers, cross-version migration, macOS 13 execution, Gatekeeper or notarization.

## Earlier fully accepted image and Keychain boundary

The frozen image is `.cache/distribution/overkill-0.1.0-dev-arm64-XvnHeb/Overkill-0.1.0-development-arm64.dmg`, SHA-256 `87898ccd8a39bd6fe7b20c1fab069495e9aa5c58e46c83185e5095911781952d`. It includes replacement-key recovery, inert `.go.txt` notice copies and persistent agent-action errors across background refresh. It built and passed native loader/signature checks, and full installed acceptance **passed on 2026-10-22** after the exact fixed copy was authorized. Its `installed-acceptance.json` binds this result to SHA-256 `87898ccd8a39bd6fe7b20c1fab069495e9aa5c58e46c83185e5095911781952d`; `artifact.json` records the passing bounded development acceptance. The installed scenario completed in 17 seconds, then image cleanup completed successfully.

The user authorized access to the development test-key item. Keychain Access was scoped to `Overkill Development Safe Storage` and this exact app; the broader “allow all applications” setting stayed off. The computer-use tool explicitly refuses Apple's SecurityAgent, so native confirmation remains a manual step. Approval for the earlier `VahecK` image was verified in the saved access list and its next test reached an authenticated, ready backend. That run then exposed a peer-panel timing problem, now repaired: the synthetic invalid target is submitted atomically and background refresh cannot clear a failed action. Source acceptance explicitly verifies error retention through refresh. The frozen image has a different ad-hoc code hash and was staged for its own manual approval; no credential was written to project files or test logs and no security bypass was used.

A prior installed test exposed an invisible-window startup hang while waiting for Keychain decryption. The current implementation creates the window first, reads public settings without decrypting secrets or waiting behind a blocked secret read, and bounds each Keychain operation to 15 seconds. A late result cannot start a backend or publish credentials after timeout; entering a replacement key does not require decrypting an inaccessible old key. Backend stop also fences a pending startup before process spawn. Development packaging uses its own product identity rather than the source app's credential namespace. Stable release signing and actual upgrade authorization remain separate work.

The current source desktop suite passes **18 tests with zero skips**, including a real native window held behind a controlled blocked-Keychain fixture, timeout/recovery state, late-result handling, and stop-during-startup. Log: `.cache/desktop-keychain-tests.log`. Packaging validates all input resource/Electron checksums and notice hashes, checks for uninventoried native/runtime files, signs copied binaries, audits signed loader dependencies, and records per-source-file hashes. Copied dependency inventory hashes describe the validated pre-signing input; each build's separate resource inventory describes the signed copies.

The final approval was initially staged for the build-folder copy while each test installed into a random temporary path. That path mismatch was a test-workflow error. The test now supports the owned fixed directory `.cache/desktop-installed-test/Installed Apps With Spaces`; its installed app is the entry currently staged in Keychain Access. Fixed-path runs reuse an existing app only after strict deep signature verification and matching the mounted image's signature hash. The native scenario still performs its later same-version replacement. Earlier attempts opened the window but timed out during encrypted setup. Keychain Access subsequently reported unsaved changes; after Save was submitted and the user completed the native dialog, the fixed-copy test passed encrypted setup, actual file effects, cancellation, peer exchange, same-version replacement, durable relaunch and database recovery. The successful script exited zero after detaching the image and writing the checksum-bound report.

## Reproduce

```sh
node scripts/prepare-desktop.mjs
node scripts/package-desktop-dev.mjs
node scripts/test-installed-desktop.mjs /absolute/path/to/development.dmg
```

For a manually authorized fixed installation, first prepare it with `OVERKILL_TEST_INSTALL_DIRECTORY=/absolute/disposable/directory node scripts/test-installed-desktop.mjs /absolute/path/to/development.dmg --install-only`. Authorize that exact installed app in macOS, then repeat with the same environment and omit `--install-only`. Existing nonempty directories require the test ownership marker; a differing signature requires explicit preparation again.

Packaging fails if the settled dependency snapshot no longer matches its input files; refresh the inventory after backend/dependency changes. An installed acceptance report is written only after the scenario and image cleanup succeed. A passing report remains scoped to its checksum and host, with public distribution readiness false.

That earlier image remains frozen. The current image below includes later backend and desktop source changes; its acceptance is separately scoped.

## Current source image: bounded installed smoke

The new frozen development image is `.cache/distribution/overkill-0.1.0-dev-arm64-sCqj16/Overkill-0.1.0-development-arm64.dmg`, SHA-256 `9b09d835f6713d3face3c8d749e0a2477ab6c3cf57955440d9350d676f33f7b8`. It contains the current backend, daemon migration safety, task dependencies/contract pins/review, repository-scoped peers and readable coordination history. The packager verified its dependency inputs, signed copies ad hoc, audited native dependencies and recorded source hashes.

On macOS 26.5.2 arm64, `installed-mock-storage-smoke.json` passed at 2026-10-22T22:15:18Z. The unchanged packaged app was copied to a unique disposable installation path with spaces. Actual bundled backend/database startup, workspace file writing, provider cancellation, frontend/backend peer exchange, same-version replacement, persisted chat/correspondence and database crash recovery passed. The signature was verified again after testing. The old accepted installed copy was untouched.

This explicitly selected smoke mode injects fake-only safeStorage methods into the disposable test process. It performs no real encryption or Keychain authorization. Relaunch restores disposable settings and explicitly reconnects; automatic credential startup remains untested for this image. The report and artifact metadata keep real installed Keychain acceptance pending, `installedAcceptance: pending` and `distributionReady: false`. The default installed test still requires actual encryption. Three subprocess guard tests reject smoke mode without mandatory test mode and an absolute installed executable.

```sh
node scripts/test-installed-desktop.mjs /absolute/path/to/development.dmg --mock-storage-smoke
```

Current source gates passed separately: 107 Go race packages (eight no-test packages), vet, Windows cross-compilation and all 64 mandatory desktop checks with zero skips. The native source test uses the default real Keychain path. This source result does not transfer Keychain acceptance to a separately signed packaged executable. Logs: `.cache/final-dmg-full-race.log`, `.cache/final-dmg-vet.log`, `.cache/final-dmg-windows.log`, `.cache/final-dmg-desktop.log`, `.cache/installed-mock-storage-smoke-current.log`.

No paid-provider, cross-version migration, minimum-macOS, Gatekeeper, Developer ID or notarization acceptance is inferred.
