# Desktop dependency and license evidence

This is a factual inventory of the **current development resource snapshot**, not a legal opinion, release certification or final DMG inventory. The JSON inventory and notice manifest deliberately retain **`distribution_ready: false`**. Local development packaging is a separate acceptance step; copying notices does not resolve the source obligations below.

The accompanying [JSON inventory](desktop-dependencies.json) records every file/hash, version observation, source URL, notice provenance, download result and open requirement. [The notice directory](../desktop/notices/README.md) contains the actual texts rather than a wrapper-license summary.

## What was inspected

- All **986 prepared resource files**, including **118 Mach-O binaries**, PostgreSQL data/extension advertisements, and the current Go backend.
- All **274 current Electron distribution files**, and all **498 entries** in the actual shipped `LICENSES.chromium.html`.
- All **76 modules embedded in the Go backend**, with exact versions, Go checksums, origin commits, module ZIP SHA-256 and corresponding notice files. Every collected module notice matches the corresponding ZIP member; no notice or module-checksum mismatch was found.
- **36 top-level PostgreSQL dylib paths**, including duplicate aliases, and **78 PostgreSQL extension/module paths**. The two non-core EDB add-ons are identified separately below.
- **209 copied notice/source-evidence files**, each with provenance and SHA-256 in `desktop/notices/manifest.json`.

The prepared backend matches the resource manifest's recorded checksum. Electron's cached `electron-v45.0.0-darwin-arm64.zip` matches the official release SHA-256 list. This authenticates the inspected candidate artifact identity; it does not establish final app signing or license completeness. Rebuilds and final app assembly require a fresh hash comparison.

The pinned PostgreSQL Maven JAR is `embedded-postgres-binaries-darwin-arm64v8:17.11.0`, SHA-256 `a1c2786acb0c398f9b2d76806fc52f5dc8b222cbc8e9383a9b9702084daaf3a5`. Its ZIP has only a manifest and native TXZ: **no dependency notice texts**. The exact [Zonky v17.11.0 recipe](https://github.com/zonkyio/embedded-postgres-binaries/blob/c56022003f8b1a6284567c365f44738b7e83514b/scripts/repack-postgres.sh) repacks EDB binaries and selects native files without the vendor's top-level license files. Zonky's Apache license therefore must not be assigned to every contained native component.

The source EDB archive is [postgresql-17.11-1-osx-binaries.zip](https://get.enterprisedb.com/postgresql/postgresql-17.11-1-osx-binaries.zip). Bounded HTTP ZIP ranges recovered its server/third-party notices without executing or downloading the full 436 MB package. `libiconv.2`, `libintl.8`, `libedit.0` and `libuuid.1.1` from that archive each match the prepared library **byte for byte**; hashes are in the JSON. Those notices are included under `native-vendor/`.

## Native component identities

Version observations below identify the available evidence. They do not assert reproducible vendor builds or the absence of vendor patches.

| Shipped component | Observable version | Primary text/source evidence | Remaining evidence |
| --- | --- | --- | --- |
| PostgreSQL, libpq, core/contrib and data | 17.11 | [Official 17.11 source](https://ftp.postgresql.org/pub/source/v17.11/postgresql-17.11.tar.bz2), copied COPYRIGHT and vendor server license | Vendor changes/build correspondence; data/per-file notices retained where present |
| OpenSSL libssl/libcrypto | 3.5.7, binary string dated 9 June 2026 | [Release LICENSE](https://github.com/openssl/openssl/blob/openssl-3.5.7/LICENSE.txt), Apache 2.0 | Vendor build provenance |
| ICU i18n/uc/data | 68.2, filenames and binary string | [68.2 LICENSE](https://github.com/unicode-org/icu/blob/release-68-2/icu4c/LICENSE), including bundled data terms | Vendor build provenance |
| Zstandard | 1.5.7, filename/string | [BSD LICENSE](https://github.com/facebook/zstd/blob/v1.5.7/LICENSE) and [GPL alternative](https://github.com/facebook/zstd/blob/v1.5.7/COPYING) both retained | Record chosen licensing route; no blanket GPL-only label |
| LZ4 library | 1.10.0, filename/string | [Library BSD text](https://github.com/lz4/lz4/blob/v1.10.0/lib/LICENSE) | Vendor build provenance |
| zlib | 1.3.2, filename/string | [Release LICENSE](https://github.com/madler/zlib/blob/v1.3.2/LICENSE) | Vendor build provenance |
| libxml2 | 2.15.3, embedded `21503` string | [Release Copyright](https://github.com/GNOME/libxml2/blob/v2.15.3/Copyright), MIT-style with per-file copyright exceptions | Preserve per-file attributions; initial `COPYING` URL was 404, resolved by `Copyright` |
| MIT Kerberos and support libraries | 1.22.2, explicit `KRB5_BRAND` string | [Exact release NOTICE](https://github.com/krb5/krb5/blob/krb5-1.22.2-final/NOTICE), including component terms | Vendor build provenance |
| GNU libiconv library | 1.19 | Read-only arm64 symbol data: `__libiconv_version = 0x0113`; matches official `iconv.h.in`. [Source archive](https://ftp.gnu.org/gnu/libiconv/libiconv-1.19.tar.gz) and LGPL 2.1-or-later selection header retained | Vendor patches/build configuration and applicable library source/replacement mechanism |
| GNU gettext runtime/libintl | 0.26 | Read-only symbol data: `_libintl_version = 0x001a00`; matches official runtime header. [Source archive](https://ftp.gnu.org/gnu/gettext/gettext-0.26.tar.gz) and LGPL 2.1-or-later selection header retained | Same vendor source/build/replacement gap; gettext tools' GPL must not be confused with the shipped runtime library |
| libedit | **Exact source version unknown** | ABI filename/current-version metadata and NetBSD helper string; EDB vendor notice groups it with zstd | ABI is not an upstream release. Vendor grouping omits a complete, clearly attributable libedit copyright set |
| libuuid | **Exact source version unknown** | ABI `1.1`; `uuid_generate` API family. EDB groups uuid with LGPL 2.1 | Do not infer an upstream release or replace the vendor's licensing statement with an assumed generic UUID license |
| EDB system_stats | Control/API version 4.0 | Shipped control file; [official v4.0 candidate license](https://github.com/EnterpriseDB/system_stats/blob/a73ec00d80bea091db28f0a0786d3d2a8a7a4bdd/LICENSE), PostgreSQL-style EDB terms | Exact binary build commit not established |
| EDB plugin_debugger / pldbgapi | Protocol/control 1.1; project release **unknown** | [Official candidate header](https://github.com/EnterpriseDB/pldebugger/blob/1dd9a9abfc65e6e905f61ff2731f871500f9b82a/plugin_debugger.c) selects Artistic 2.0; full canonical text retained | Binary embeds PostgreSQL 17.10 build string. Protocol 1.1 is not the add-on's source release. Exact build revision and matching notices need confirmation |

The two EDB add-ons are not covered simply by a vanilla PostgreSQL source archive. The inspected `pldbgapi.control` also names `$libdir/pldbgapi`, while the shipped binary is `plugin_debugger.dylib`; that is a separate optional-feature correctness observation, not a license conclusion.

No evidence here establishes that EDB's proprietary Limited Use License governs the community PostgreSQL bytes. Vendor origin alone does not establish that. The matching archive's actual open-source notices, the add-ons' notices and the unknown build details are recorded separately.

## Electron and embedded code

The actual [Electron 45.0.0 release](https://releases.electronjs.org/release/v45.0.0) specifies Chromium **156.0.8078.48**, Node **24.22.0** and V8 **15.6.75.19**. Its MIT LICENSE and complete Chromium credits were copied from the installed distribution. The exact Node 24.22.0 LICENSE was fetched and matches the Node credit text checksum.

All 498 credit entries, their homepage, notice-text hash and known/unknown version evidence are indexed in the JSON. This includes fonts, codecs and native frameworks as listed in the shipped notice. Most credit entries do not print their own version; their detailed upstream revisions remain unmapped rather than being invented. Electron/Chromium's pinned DEPS are archived as source evidence.

FFmpeg is a concrete source-obligation case. Its shipped credit includes LGPL terms. Chromium's pinned DEPS identifies revision `9db86ce5b5b454dfc96c435f9f0723012980c37f`; the [matching source archive](https://chromium.googlesource.com/chromium/third_party/ffmpeg/+archive/9db86ce5b5b454dfc96c435f9f0723012980c37f.tar.gz) was downloaded and hashed. Its `README.chromium` lists the source license families and Chromium-specific configuration/patch locations. This source retrieval does **not** yet establish all Electron-specific patches/build settings, recipient source access, or a tested library-replacement path in the final signed app.

## Go backend and source obligations

`go version -m` reports Go **1.26.10**, `CGO_ENABLED=0`, Darwin arm64, and 76 dependency modules. The complete per-module table is in the JSON; the copied notices retain module identity/version and nested third-party notices. The Go runtime/toolchain notices are a conservative source-tree notice superset, not a claim that every compiler dependency ships in the backend.

The binary contains **593 matching libsignal symbols**, including cipher/protocol implementations, in addition to its module metadata. The module is `go.mau.fi/libsignal v0.2.1`, origin commit `6e0a3f2435edc0d16c4c328536eca8b940cfa1be`, and its actual LICENSE is **GNU GPL version 3**. It is not the differently licensed Signal/Rust or unrelated Go implementation. Its exact cached source ZIP and license are retained. This is a substantive combined-backend distribution/source question; top-level application MIT/Apache notices do not resolve it. GPLv3's [source/non-source conveyance sections](https://www.gnu.org/licenses/gpl-3.0.html#section6) are the primary obligation evidence.

`go.mau.fi/util v0.9.8` and `go.mau.fi/whatsmeow v0.0.0-20260513140310-c551a4055c0f` have **MPL 2.0** licenses. Their exact source ZIPs are also retained. MPL's [executable-form provisions](https://www.mozilla.org/en-US/MPL/2.0/#distribution-of-executable-form) describe covered-source availability and recipient notices. A source ZIP in an ignored local research directory is not itself a recipient-facing distribution mechanism.

For the native LGPL libraries and FFmpeg, [LGPL 2.1 sections 4 and 6](https://www.gnu.org/licenses/old-licenses/lgpl-2.1.html) provide the relevant library/source/combination conditions. Actual vendor changes and build/installation materials, the chosen source delivery route, and applicable replacement/relinkability behavior still need evidence. Downloading a nominal upstream release is useful but is not proof of complete corresponding vendor source.

## Concrete remaining release evidence

1. A distribution/source decision for the actual GPL-bearing static backend, with matching application source, dependency sources, build materials and recipient access instructions. No combined corresponding-source package was prepared by this research step.
2. Recipient-facing covered-source notices for the MPL modules and chosen source/replacement provisions for LGPL libraries/FFmpeg. Exact source archives already retained are inputs, not a certification.
3. Exact libedit/libuuid source versions and correct attributions; vendor patch/build correspondence for the native LGPL binaries.
4. Exact EDB add-on build identities/notices, or validated removal of unused add-ons and their SQL/control advertisements. No resources were removed here.
5. Detailed revision mapping of remaining Electron credit components where release-specific evidence is required, plus final verification that notices/source instructions actually ship.
6. New hashes after the final backend build and app/DMG assembly; final installation, signing/notarization and any library-replacement acceptance are separate checks.

Potential pruning is an engineering option, not a conclusion drawn from missing direct loader references. `system_stats` and `plugin_debugger` are optional loadable add-ons, and other modules can be loaded dynamically by SQL/preload settings. A safe removal decision must inspect the supported feature/configuration surface and remove matching advertisements, then retest the private database. No pruning, application/resource mutation, signing, packaging or external publishing occurred in this task.

Read-only research artifacts and exact downloads are under `research/desktop-dependencies/`; they include failed retrievals, the recovered EDB ZIP directory, native version evidence, byte-match receipts and source archive hashes. No downloaded upstream code was executed.
