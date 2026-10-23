"""Protect coverage/provenance ledger invariants; do not certify feature truth."""
import copy
import importlib.util
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location('harness_normalization', Path(__file__).resolve().parents[1] / 'harness_normalization.py')
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class NormalizationValidationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        (self.root / 'source.txt').write_text('declaration\nconsumer\n')
        self.snapshot = self.root / 'research/harnesses/fixture/source'
        self.snapshot.mkdir(parents=True)
        (self.snapshot / 'source.txt').write_text('declaration\nconsumer\n')
        self.evidence = {'url': 'https://upstream.example/pinned/source#L1', 'path': 'source.txt', 'line': 1}
        self.original = {'source': {'commit': 'pinned'}, 'features': [{'id': value} for value in ('flag.v', 'flag.alias', 'heading')]}
        self.report = {'schema_version': 1, 'harness': 'fixture', 'source': {'commit': 'pinned'},
                       'capabilities': [{'id': 'verbose', 'name': 'Verbose output', 'category': 'observability/cost/evals',
                                         'upstream': {'status': 'implemented', 'source_record_ids': ['flag.v', 'flag.alias'], 'evidence': [self.evidence]},
                                         'overkill': {'status': 'unassessed', 'reason': 'No matching runtime behavior reviewed.'}}],
                       'raw_record_dispositions': [
                           {'source_record_id': 'flag.v', 'disposition': 'capability', 'capability_ids': ['verbose'], 'reason': 'Declaration and consumer traced.', 'evidence': [self.evidence]},
                           {'source_record_id': 'flag.alias', 'disposition': 'duplicate', 'capability_ids': ['verbose'], 'reason': 'Alias dispatches same flag consumer.', 'evidence': [self.evidence]},
                           {'source_record_id': 'heading', 'disposition': 'nonfeature', 'capability_ids': [], 'reason': 'Navigation title, no behavior.', 'evidence': [self.evidence]}]}

    def validate(self, report=None):
        errors, stale = [], set()
        summary, capabilities = MODULE.validate_normalization(self.root, 'fixture', report or self.report, self.original, errors, {}, stale)
        return errors, summary, capabilities, stale

    def test_complete_partition_keeps_metadata_distinct_from_features(self):
        errors, summary, capabilities, _ = self.validate()
        self.assertEqual(errors, [])
        self.assertTrue(summary['original_id_partition_complete'])
        self.assertEqual(summary['semantic_capabilities_and_variants'], 1)
        self.assertEqual(summary['disposition_counts'], {'capability': 1, 'duplicate': 1, 'nonfeature': 1})
        self.assertEqual(capabilities[0]['overkill_status'], 'unassessed')

    def test_missing_duplicate_and_dangling_records_fail(self):
        for mutation in ('missing', 'duplicate', 'dangling', 'backlink'):
            with self.subTest(mutation=mutation):
                report = copy.deepcopy(self.report)
                if mutation == 'missing':
                    report['raw_record_dispositions'].pop()
                elif mutation == 'duplicate':
                    report['raw_record_dispositions'].append(copy.deepcopy(report['raw_record_dispositions'][0]))
                elif mutation == 'dangling':
                    report['raw_record_dispositions'][0]['capability_ids'] = ['invented']
                else:
                    report['capabilities'][0]['upstream']['source_record_ids'] = ['flag.v']
                self.assertTrue(self.validate(report)[0])

    def test_missing_and_invalid_evidence_fails(self):
        for mutation in ('url', 'line', 'path', 'escape'):
            with self.subTest(mutation=mutation):
                report = copy.deepcopy(self.report)
                evidence = report['capabilities'][0]['upstream']['evidence'][0]
                evidence.update({'url': 'http://invalid.example'} if mutation == 'url' else
                                {'line': 999} if mutation == 'line' else
                                {'path': 'absent.txt'} if mutation == 'path' else
                                {'path': '../outside-root.txt'})
                self.assertTrue(self.validate(report)[0])

    def test_upstream_hash_drift_is_invalid_provenance(self):
        report = copy.deepcopy(self.report)
        report['capabilities'][0]['upstream']['evidence'][0]['sha256'] = '0' * 64
        self.assertTrue(any('upstream snapshot hash changed' in error for error in self.validate(report)[0]))

    def test_changed_verified_anchor_is_unknown_not_recertified(self):
        report = copy.deepcopy(self.report)
        report['capabilities'][0]['overkill'] = {'status': 'verified', 'reason': 'Prior bounded acceptance.',
                                               'production_evidence': [{'path': 'source.txt', 'line': 1, 'sha256': '0' * 64}]}
        errors, _, capabilities, stale = self.validate(report)
        self.assertEqual(errors, [])
        self.assertEqual(capabilities[0]['overkill_status'], 'unknown')
        self.assertEqual(stale, {'source.txt'})

    def test_supplemental_discoveries_have_independent_exact_backlinks(self):
        report = copy.deepcopy(self.report)
        report['source_discoveries'] = [{'id': 'new.verbose', 'kind': 'cli-option', 'status': 'implemented', 'path': 'source.txt', 'line': 2, 'capability_ids': ['verbose']}]
        report['capabilities'][0]['upstream']['source_discovery_ids'] = ['new.verbose']
        errors, summary, _, _ = self.validate(report)
        self.assertEqual(errors, [])
        self.assertEqual(summary['discovery_records'], 3)
        self.assertEqual(summary['supplemental_source_discoveries'], 1)
        for mutation in ('missing_backlink', 'dangling', 'duplicate', 'unmapped', 'line'):
            with self.subTest(mutation=mutation):
                broken = copy.deepcopy(report)
                if mutation == 'missing_backlink':
                    broken['capabilities'][0]['upstream']['source_discovery_ids'] = []
                elif mutation == 'dangling':
                    broken['source_discoveries'][0]['capability_ids'] = ['missing']
                elif mutation == 'duplicate':
                    broken['source_discoveries'].append(copy.deepcopy(broken['source_discoveries'][0]))
                elif mutation == 'unmapped':
                    broken['source_discoveries'][0]['capability_ids'] = []
                else:
                    broken['source_discoveries'][0]['line'] = 999
                self.assertTrue(self.validate(broken)[0])

    def test_declared_excerpt_spans_match_the_actual_snapshot(self):
        report = copy.deepcopy(self.report)
        anchor = report['capabilities'][0]['upstream']['evidence'][0]
        anchor.update(inspection_start_line=1, end_line=2, excerpt='declaration\nconsumer')
        self.assertEqual(self.validate(report)[0], [])
        anchor['excerpt'] = 'invented declaration\nconsumer'
        self.assertTrue(any('excerpt differs' in e for e in self.validate(report)[0]))
        anchor['end_line'] = 999
        self.assertTrue(any('invalid inspection span' in e for e in self.validate(report)[0]))

    def test_upstream_and_production_paths_keep_their_declared_owner(self):
        (self.root / 'README.md').write_text('Overkill\n')
        (self.snapshot / 'README.md').write_text('Harness\nsecond line\n')
        self.assertEqual(MODULE.resolve_evidence_path(self.root, 'fixture', 'README.md'),
                         self.snapshot / 'README.md')
        self.assertEqual(MODULE.resolve_evidence_path(self.root, 'fixture', 'README.md', production=True),
                         self.root / 'README.md')
        self.assertEqual(MODULE.resolve_evidence_path(self.root, 'fixture', 'research/harnesses/fixture/source/README.md'),
                         self.snapshot / 'README.md')
        report = copy.deepcopy(self.report)
        report['capabilities'][0]['upstream']['evidence'][0].update(
            path='README.md', line=2, inspection_start_line=1, end_line=2,
            excerpt='Harness\nsecond line')
        self.assertEqual(self.validate(report)[0], [])

    def test_missing_or_escaping_upstream_file_cannot_use_workspace_shadow(self):
        (self.root / 'README.md').write_text('Overkill\n')
        self.assertIsNone(MODULE.resolve_evidence_path(self.root, 'fixture', 'README.md'))
        (self.snapshot / 'README.md').symlink_to(self.root / 'README.md')
        self.assertIsNone(MODULE.resolve_evidence_path(self.root, 'fixture', 'README.md'))
        self.assertIsNone(MODULE.resolve_evidence_path(self.root, 'fixture', '../source.txt'))


if __name__ == '__main__':
    unittest.main()
