# Preserve settings drafts during startup

The installed-app smoke initially failed during a screenshot; an isolated rerun
then failed because provider setup reported an empty API key. The retained
failure screenshot showed the dialog open with the entered model/endpoint and
an empty key. Those are separate observations; no cause is asserted for the
first screenshot closure.

Source inspection found `showSettings()` always populated fields after awaiting
public settings, even when another startup state callback had already opened
the dialog. A deterministic regression holds two real renderer-function state
reads, opens the first dialog, enters a draft, then releases the stale second
read. Before repair the stale model replaces the draft and the key is cleared.
The guard now preserves an already open dialog both before and after the await.
Ordinary submit/close still clears the transient key as before.

Receipts: `.cache/settings-dialog-race-before.log` (reproduced failure) and
`.cache/settings-dialog-race-after.log` (passing regression). The test runs the
actual renderer function with controlled deferred public-state reads and checks
model, endpoint, key and workspace guidance. The full mandatory desktop gate and
new installed candidate are rerun separately after this repair. No real
credentials or Keychain permissions are part of the regression.

Installed evidence is intentionally bounded: BmvEFh first failed at screenshot
capture and then at empty-key setup; XqJEnw initially also failed empty-key setup.
After adding submit-key-length-only diagnostics and immediate setup-error
reporting, XqJEnw passed twice, including a fast fill→click rerun without an
extra field-read delay. Receipts: `.cache/install-storage-installed-diagnostic.log`
and `.cache/install-storage-installed-fast-repeat.log`. Earlier failures remain
in `.cache/install-storage-installed-smoke*.log`. The guarded deferred-read bug
is proven and fixed; those final passes do not prove a unique cause for every
intermittent native setup failure. The broader interactive/release gate remains
open. Diagnostics never record entered key content.
