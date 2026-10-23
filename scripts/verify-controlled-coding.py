#!/usr/bin/env python3
"""Mandatory local coding/coordination acceptance; only disposable fixture providers.

Requires an explicitly configured local PG_TEST_URL and prepared desktop
resources. Runs only named fake-storage native fixtures, never native.test.cjs
(the separate real-Keychain/installed-app gate). No paid provider is contacted.
"""
import argparse
import json
import hashlib
import os
from pathlib import Path
import re
import signal
import subprocess
import time
from urllib.parse import urlparse

ROOT = Path(__file__).resolve().parents[1]
CASES = [
    'TestCoordinationAutomaticDeliveryUsesBoundSessionAndWakesSender',
    'TestCoordinationApplicationControlsUseDurableIdentity',
    'TestCoordinationEventsReplayAcrossServerRestart',
    'TestCallbackAuthenticatedApplicationRPCAndOwnedDelivery',
    'TestCallbackSignedReplayPeerFilteringAndRestart',
    'TestCallbackAcceptedHTTPBeforeCrashUsesSameDeliveryIdentity',
    'TestCallbackBackoffDeadLetterAndExplicitRetry',
    'TestCallbackCancellationAndShutdownOwnAttempt',
    'TestCallbackLostSecretRotationFencesOldOwnerAndRevocationAllowsResubscribe',
    'TestDispatcherLeavesBusySessionUnclaimed',
    'TestDispatcherCancellationStopsOwnedExecution',
    'TestExpiredWorkerRequiresReviewAndCannotCommitLateResult',
    'TestConcurrentSendRetriesCreateOneReceiptAndEvent',
    'TestCredentialAndProjectBoundaries',
    'TestDependenciesFailureCascadeIsDurableAndNeverExecutes',
    'TestDependenciesDeadlineAndLeaseFencing',
    'TestClaimsProgressRepliesAndReplaySurviveRestart',
]


def run(command, environment, log, timeout):
    started = time.monotonic()
    with log.open('w') as output:
        child = subprocess.Popen(command, cwd=ROOT, env=environment,
                                 stdout=output, stderr=subprocess.STDOUT,
                                 start_new_session=True)
        try:
            code = child.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            os.killpg(child.pid, signal.SIGTERM)
            try:
                child.wait(timeout=20)
            except subprocess.TimeoutExpired:
                os.killpg(child.pid, signal.SIGKILL)
                child.wait()
            raise RuntimeError(f'Acceptance timeout; inspect {log}')
    if code:
        raise RuntimeError(f'Acceptance failed ({code}); inspect {log}')
    return round(time.monotonic() - started, 3)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', default='go')
    parser.add_argument('--node', default='node')
    parser.add_argument('--output', type=Path, default=ROOT / '.cache/controlled-coding-acceptance')
    args = parser.parse_args()
    database = os.environ.get('PG_TEST_URL', '')
    if urlparse(database).hostname not in {'localhost', '127.0.0.1', '::1'}:
        parser.error('PG_TEST_URL must explicitly identify a local disposable test database')
    args.output.mkdir(parents=True, exist_ok=True)
    source_names=['main.cjs','preload.cjs','renderer.js','index.html','style.css','clarification.js','interaction-input.cjs','stop-marker.cjs']
    source_hashes={name:hashlib.sha256((ROOT/'desktop/src'/name).read_bytes()).hexdigest() for name in source_names}
    report = {'desktop_source_sha256':source_hashes,'scope': 'Controlled local providers and disposable real application/API/PostgreSQL',
              'real_paid_provider_executed': False, 'keychain_executed': False,
              'installed_app_modified': False, 'provider_billing_truth_claimed': False,
              'real_provider_gate': 'pending opt-in live-provider acceptance',
              'credential_scope': 'Disabled/revoked peer credentials and launch credentials; no time-expiring peer credential mechanism is claimed',
              'passes': {}, 'complete': False}
    try:
        env = dict(os.environ, OVERKILL_REQUIRE_TEST_DB='1')
        go_log = args.output / 'coordination.jsonl'
        elapsed = run([args.go, 'test', '-json', '-race', '-count=1', '-timeout=5m',
                       './internal/api', './internal/coordination', '-run', '^(' + '|'.join(CASES) + ')$'],
                      env, go_log, 360)
        events = [json.loads(line) for line in go_log.read_text().splitlines() if line.startswith('{')]
        passed = {item.get('Test') for item in events if item.get('Action') == 'pass'}
        missing = set(CASES) - passed
        skipped = [item.get('Test') for item in events if item.get('Action') == 'skip']
        if missing or skipped:
            raise RuntimeError(f'Mandatory cases missing/skipped: {sorted(missing)} / {skipped}')
        report['passes']['coordination'] = {'tests': CASES, 'skips': 0, 'seconds': elapsed, 'log': str(go_log)}
        native_log = args.output / 'native.tap'
        env['OVERKILL_REQUIRE_DESKTOP_TEST'] = '1'
        elapsed = run([args.node, '--test', '--test-reporter=tap', '--test-concurrency=1',
                       'desktop/test/clarification.test.cjs',
                       'desktop/test/stop-marker.test.cjs',
                       'desktop/test/interactive-coding-native.test.cjs',
                       'desktop/test/workspace-native.test.cjs'], env, native_log, 420)
        text = native_log.read_text()
        if not re.search(r'^# fail 0$', text, re.M) or not re.search(r'^# skipped 0$', text, re.M):
            raise RuntimeError('Mandatory native pass/zero-skip receipt missing')
        report['passes']['native'] = {'seconds': elapsed, 'skips': 0, 'log': str(native_log),
                                     'storage': 'Disposable fake credentials; real application runtime/database/tools',
                                     'coding_artifacts': str(ROOT / '.cache/interactive-coding-artifacts'),
                                     'rendered_frontend': str(ROOT / '.cache/interactive-authored-users-ui.png')}
        report['complete'] = True
    finally:
        (args.output / 'receipt.json').write_text(json.dumps(report, indent=2) + '\n')
    print('Controlled native coding + 17 real-PG coordination boundary cases PASS; live-provider gate remains pending')


if __name__ == '__main__':
    main()
