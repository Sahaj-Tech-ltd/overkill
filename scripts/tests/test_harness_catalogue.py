"""Explicit original comparison provenance and publication boundaries."""
import hashlib
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).parents[1]))
spec = importlib.util.spec_from_file_location('harness_catalogue', Path(__file__).parents[1] / 'build-harness-catalogue.py')
catalogue = importlib.util.module_from_spec(spec)
spec.loader.exec_module(catalogue)


class OriginalAssessmentTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        (self.root / 'source.go').write_text('package fixture\n// compared\n')
        data = (self.root / 'source.go').read_bytes()
        self.anchor = {'path': 'source.go', 'line': 1, 'end_line': 1,
                       'sha256': hashlib.sha256(data).hexdigest(),
                       'excerpt_sha256': hashlib.sha256(b'package fixture\n').hexdigest()}
        self.entry = {'harness': 'test', 'id': 'original', 'report': 'test.json', 'overkill_status': 'unassessed'}
        self.errors, self.stale = [], set()

    def tearDown(self):
        self.tmp.cleanup()

    def apply(self, assessment):
        feature = {'id': 'original'}
        if assessment is not None:
            feature['overkill'] = assessment
        catalogue.apply_original_assessment(feature, self.entry, self.root, self.errors, self.stale)

    def test_no_explicit_assessment_remains_unassessed(self):
        self.apply(None)
        self.assertEqual(self.entry['overkill_status'], 'unassessed')
        self.assertNotIn('parity_report', self.entry)

    def test_exact_assessment_links_owning_original_report(self):
        self.apply({'status': 'partial', 'reason': 'Only selected subset', 'production_evidence': [self.anchor]})
        self.assertEqual(self.errors, [])
        self.assertEqual(self.entry['overkill_status'], 'partial')
        self.assertEqual(self.entry['parity_report'], 'test.json')
        self.assertEqual(self.entry['parity_evidence_status'], 'snapshot-current')

    def test_changed_source_demotes_verified_and_marks_partial_stale(self):
        (self.root / 'source.go').write_text('package changed\n// compared\n')
        for status in ['partial', 'verified']:
            self.apply({'status': status, 'reason': 'comparison', 'production_evidence': [self.anchor]})
            self.assertEqual(self.entry['overkill_status'], 'unknown' if status == 'verified' else status)
            self.assertEqual(self.entry['parity_evidence_status'], 'source-changed-review-required')
        self.assertEqual(self.stale, {'source.go'})

    def test_bad_hashes_spans_and_escaping_paths_are_errors(self):
        for patch in [{'sha256': None}, {'end_line': 999}, {'path': '../outside'}]:
            before = len(self.errors)
            self.apply({'status': 'partial', 'reason': 'comparison', 'production_evidence': [dict(self.anchor, **patch)]})
            self.assertGreater(len(self.errors), before)
        self.apply({'status': 'verified', 'reason': 'unsupported'})
        self.assertIn('invalid or missing production evidence', self.errors[-1])

    def test_existing_parity_package_takes_precedence(self):
        # Run the real builder against one temporary reviewed package. A conflicting
        # inline comparison must not replace the accepted package's assessment.
        reports = self.root / 'docs/harness-research'
        reports.mkdir(parents=True)
        feature = {'id': 'capability.one', 'name': 'one', 'category': 'context', 'status': 'documented',
                   'evidence': [{'url': 'https://example.com/docs'}],
                   'overkill': {'status': 'missing', 'reason': 'inline'}}
        (reports / 'test.json').write_text(json.dumps({'name': 'test', 'source': {}, 'deepwiki': {'status': 'none'}, 'features': [feature]}))
        (reports / 'test-parity.json').write_text(json.dumps({'records': [{'id': 'capability.one', 'status': 'partial', 'reason': 'accepted package'}]}))
        for slug in ('cline', 'hermes-agent', 'openclaw'):
            (reports / (slug + '.json')).write_text(json.dumps({'features': []}))
            (reports / (slug + '-capabilities.json')).write_text(json.dumps({
                'coverage': {'mapped_discovery_record_ids': [], 'remaining_uninspected_discovery_record_ids': []},
                'category_counts': {str(i): 0 for i in range(12)}, 'capabilities': []}))
        from unittest.mock import patch
        with patch.object(catalogue, 'ROOT', self.root), patch.object(catalogue, 'REPORTS', reports), patch.object(catalogue, 'SLUGS', ('test',)), patch.object(catalogue, 'ACCEPTED_PARITY', {'test': None}):
            # Supplemental capability files for the fixed optional slugs are absent.
            catalogue.main()
        result = json.loads((reports / 'catalogue.json').read_text())
        self.assertEqual(result['entries'][0]['overkill_status'], 'partial')
        self.assertEqual(result['entries'][0]['parity_report'], 'test-parity.json')


if __name__ == '__main__':
    unittest.main()
