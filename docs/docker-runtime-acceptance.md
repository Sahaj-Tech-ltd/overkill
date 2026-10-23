# Actual Docker runtime acceptance

On 2026-10-22 the current source built and ran inside an actual Linux/arm64
Docker Engine using a project-owned Lima Virtualization.framework VM. The first
build used a fresh Docker image cache. A second build and full smoke rerun used
the final updater, retention and snapshot repairs. Neither native compilation
nor a workflow definition substitutes for these executed checks.

Both `bash scripts/verify-docker.sh` runs passed:

- Compose configuration and the multistage Go/Node/Python image build.
- Fresh PostgreSQL/pgvector image startup, readiness and project-private volumes.
- Non-root runtime, installed CLI version, bundled Node, plain compiled frontend
  import, installed Python bridge dependencies and schema-generated config validation.
- Actual packaged Ink PTY startup with authenticated backend, keyboard help and
  clean exit, without a source frontend or system Node.
- API readiness with authentication and HTTP401 without authentication.
- Cleanup of only each smoke run’s own Compose stack and volumes.

Receipts: `.cache/container-runtime/docker-verification.log` (initial build) and
`docker-verification-final.log` (final source). The VM mounts only a read-only
98 MiB staged build context, not the user’s home directory. Research archives,
generated research reports, dependencies and caches are excluded by
`.dockerignore`. The owned VM is stopped after acceptance to release its memory;
its local state remains reusable. No global container runtime was installed.

The project-local Lima2.2.1 archive is pinned to SHA256
`9e9eacce88f37e185c346bad73aa6136f738d8cdf8c3bb23cd42b071824bc66e`;
its verified Ubuntu image is pinned by the selected Lima template. Acquisition
receipt: `.cache/container-runtime/downloads/lima-receipt.json`.
[Official binary installation](https://lima-vm.io/docs/installation/) and
[Docker template](https://github.com/lima-vm/lima/blob/master/README.md).

This closes the original build-input/container acceptance scope of BUILD-02
and supplies the Docker portion of BUILD-03/04 together with
[the native installer/frontend evidence](installer-release-acceptance.md).
It does not establish Linux/amd64 or native Windows acceptance, public release
publication, live provider calls, production-volume recovery or semantic model
execution. Provider-less configuration warnings remain explicit.


Current interaction-source rerun passes in `.cache/container-runtime/interaction-docker-verification-final.log`: actual image, fresh PostgreSQL, packaged Node/Ink/Python, config, PTY startup and authenticated API. Initial rerun exposed lost rapid `v` navigation increments in the Ink panel; a functional state update fixes the product behavior, and both host 42-test gate and actual container rerun pass. The initial failure is retained in `interaction-docker-verification.log`. The project-owned VM was stopped after cleanup (`interaction-lima-stop.log`).


Final transport/mention source acceptance passes in `.cache/container-runtime/transport-mentions-docker-final.log`. The initial current-source run failed an actual Node22 committed-frame/stale-input race in registration; the precise no-act regression fails before the product repair and passes with stable input forwarding published during layout commit. Both host and actual LinuxNode22 full suites pass47 tests. Test synchronization alone was not used as the repair. The retained initial log is `.cache/container-runtime/transport-multimodal-docker.log`; the project-owned VM was stopped after final cleanup.
