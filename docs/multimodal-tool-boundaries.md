# Configured image tool boundaries

The harness comparison reproduced two actual leaks with harmless local markers.
The default vision registry forwarded an external workspace file to a capture-only
describer; image generation fetched an initial provider-returned loopback URL.
The BEFORE receipts are `.cache/multimodal-vision-boundary-before.log` and
`.cache/multimodal-image-download-before.log`. No paid service was involved.

The production vision registry now supplies its canonical workspace. File mode
requires that root and traverses through `os.Root`, protecting against leaf and
parent symlink escapes during resolution. Regular images are bounded to 20 MiB;
a limit-plus-one read detects growth beyond the initial stat. Unix opens are
nonblocking so pipes are rejected before waiting for a writer. Tests exercise
actual default-registry reads, absolute/relative paths, contained symlinks,
outside symlinks, missing roots, cancellation, directories, sparse oversized
files and a real FIFO (`.cache/vision-boundary-after.log`). This does not assert
that browser screenshot/navigation is an OS or request-time network sandbox.

Provider-returned generated-image downloads now require public HTTPS on port443.
Every socket resolves and checks all DNS answers, then dials the checked address
while TLS verifies the original hostname. Proxies, connection reuse and redirects
are disabled. URL credentials/fragments and protected IPv4/IPv6 ranges fail
closed. Files are created exclusively with private permissions; a 50 MiB plus
one-byte bound rejects oversized or broken responses and removes partial output.
Tests check actual rejected provider-returned loopback URLs, mixed DNS answers,
rebinding, pinned socket addresses, real TLS hostname rejection, redirect refusal,
body failure cleanup and file permissions. Local socket fixtures inject network
resolution only inside tests; production never grants loopback downloads.

Stability responses previously reported a 4 MiB prefix as a successful image.
The response now preserves complete images up to 50 MiB and rejects overflow;
fixtures assert the actual complete output length and private permissions.
Replicate keeps its poll request context alive until the body is consumed, and
the entire create/poll flow has a five-minute context bound. A controlled
RoundTripper verifies a live context during body reads and rejection of an unsafe
returned URL. Receipts: `.cache/image-download-safety-tests-final.log` and
`.cache/multimodal-safety-focused-final.log`.

Both configured tools now advertise real argument schemas/descriptions through
the guarded model catalogue and validate bounded input fields. Tool metadata is
not proof of a native chat attachment/paste workflow: no such workflow or
automatic gateway image-caption routing is inferred. Provider protocol fixtures
do not establish upstream paid-service compatibility or image editing parity.
Operator-configured generation API endpoints are trusted configuration, separate
from the untrusted returned image URL policy. Existing installed DMGs retain
their immutable tested-source scope until a new image is prepared.
