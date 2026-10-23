import base64
import copy
import gzip
import importlib.util
import json
from pathlib import Path
import re
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('harness_explorer', Path(__file__).parents[1] / 'build-harness-explorer.py')
explorer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(explorer)


class ExplorerTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.reports = self.root / 'docs/harness-research'
        self.reports.mkdir(parents=True)
        self.catalogue = {
            'schema_version': 1, 'method': 'source only', 'limitation': 'not exhaustive', 'normalization_scope': 'overlap',
            'harnesses': [{'slug': 'test', 'records': 1}], 'complete_normalization_reports': [],
            'entries': [{'harness': 'test', 'id': 'same', 'name': '</script><script>alert(1)</script>', 'report': 'test.json', 'source_status': 'unknown', 'overkill_status': 'unassessed'}],
            'normalized_capabilities': [
                {'harness': 'test', 'id': 'same', 'name': 'normalized', 'parity_report': 'test-normalized.json', 'overkill_status': 'unknown'},
                {'harness': 'test', 'id': 'same', 'name': 'supplemental', 'parity_report': 'test-capabilities.json', 'overkill_status': 'partial'}],
            'validation': {'errors': [], 'records': 1, 'additional_normalized_capabilities': 2},
        }
        self.write('catalogue.json', self.catalogue)
        self.write('test.json', {'features': [{'id': 'same', 'description': 'unsafe </script> & data', 'evidence': [{'url': 'javascript:alert(1)', 'path': '/etc/passwd'}]}]})
        self.write('test-normalized.json', {'capabilities': [{'id': 'same', 'overkill': {'status': 'unknown'}}]})
        self.write('test-capabilities.json', {'capabilities': [{'id': 'same', 'overkill': {'status': 'partial'}}]})

    def tearDown(self):
        self.tmp.cleanup()

    def write(self, name, value):
        (self.reports / name).write_text(json.dumps(value))

    def test_all_layers_and_same_ids_across_reports_preserved(self):
        data = explorer.build_data(self.reports, self.root)
        self.assertEqual(len(data['rows']), 3)
        self.assertEqual([r['overkill_status'] for r in data['rows']], ['unassessed', 'unknown', 'partial'])
        self.assertEqual([r['id'] for r in data['rows']], ['same'] * 3)
        self.assertEqual(data['totals'], {'original': 1, 'normalized': 2})

    def test_explicit_original_assessment_reason_uses_owning_report(self):
        self.catalogue['entries'][0].update(overkill_status='missing', parity_report='test.json')
        self.write('catalogue.json', self.catalogue)
        self.write('test.json', {'features': [{'id': 'same', 'overkill': {
            'status': 'missing', 'reason': 'Exact original scope; no child inference'}}]})
        row = explorer.build_data(self.reports, self.root)['rows'][0]
        self.assertEqual(row['overkill_status'], 'missing')
        self.assertEqual(row['summaries']['assessment.reason'], 'Exact original scope; no child inference')
        self.assertEqual(row['parity_report'], row['report'])

    def test_encoded_payload_round_trips_and_cannot_close_script(self):
        output = self.reports / 'explorer.html'
        result = explorer.generate(self.reports, self.root, output)
        html = output.read_text()
        payload = re.search(r'type="application/octet-stream">([^<]+)</script>', html).group(1)
        data = json.loads(gzip.decompress(base64.b64decode(payload)))
        self.assertEqual(data['rows'][0]['name'], self.catalogue['entries'][0]['name'])
        self.assertNotIn('<script>alert(1)', html)
        self.assertEqual(result['represented_records'], 3)
        self.assertNotIn('innerHTML', html)
        self.assertIn("connect-src 'none'", html)

    def test_failed_validation_preserves_published_output(self):
        output = self.reports / 'explorer.html'
        output.write_text('previous valid artifact')
        self.catalogue['validation']['records'] = 99
        self.write('catalogue.json', self.catalogue)
        with self.assertRaisesRegex(ValueError, 'totals'):
            explorer.generate(self.reports, self.root, output)
        self.assertEqual(output.read_text(), 'previous valid artifact')
        self.assertEqual(list(self.reports.glob('.explorer-*')), [])

    def test_duplicate_identity_and_missing_record_rejected(self):
        self.catalogue['entries'].append(copy.deepcopy(self.catalogue['entries'][0]))
        self.write('catalogue.json', self.catalogue)
        with self.assertRaisesRegex(ValueError, 'duplicate'):
            explorer.build_data(self.reports, self.root)
        self.catalogue['entries'].pop()
        self.catalogue['entries'][0]['id'] = 'absent'
        self.write('catalogue.json', self.catalogue)
        with self.assertRaisesRegex(ValueError, 'Missing report record'):
            explorer.build_data(self.reports, self.root)

    def test_unsafe_links_never_become_clickable(self):
        data = explorer.build_data(self.reports, self.root)
        evidence = data['rows'][0]['evidence'][0]
        self.assertNotIn('urls', evidence)
        self.assertNotIn('local_href', evidence)
        for url in ('javascript:alert(1)', 'data:text/html,hello', 'https://example.com/\nunsafe'):
            self.assertIsNone(explorer.safe_http(url))
        self.assertIsNone(explorer.local_link('/etc/passwd', self.root, self.reports))
        self.assertIsNone(explorer.local_link('../../escape', self.root, self.reports))
        self.assertEqual(explorer.safe_http('https://example.com/docs'), 'https://example.com/docs')

    def test_report_traversal_and_catalogue_errors_rejected(self):
        self.catalogue['entries'][0]['report'] = '../unsafe.json'
        self.write('catalogue.json', self.catalogue)
        with self.assertRaisesRegex(ValueError, 'Unsafe report'):
            explorer.build_data(self.reports, self.root)
        self.catalogue['validation']['errors'] = ['bad evidence']
        self.write('catalogue.json', self.catalogue)
        with self.assertRaisesRegex(ValueError, 'validation errors'):
            explorer.build_data(self.reports, self.root)


if __name__ == '__main__':
    unittest.main()
