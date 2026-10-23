#!/usr/bin/env python3
"""Refresh Go notices and candidate hashes; reject changes to audited native inputs.

Run after prepare-desktop.mjs. This preserves unresolved source/license evidence;
it never certifies distribution readiness or substitutes for a new native audit.
"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def digest(file):
    value = hashlib.sha256()
    with file.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            value.update(chunk)
    return value.hexdigest()


def record(file, base):
    relative = file.relative_to(base).as_posix()
    if file.is_symlink():
        return {'path': relative, 'kind': 'symlink', 'target': os.readlink(file)}
    return {'path': relative, 'kind': 'file', 'bytes': file.stat().st_size, 'sha256': digest(file)}


def inventory(base):
    return [record(p, base) for p in sorted(base.rglob('*')) if p.is_file() or p.is_symlink()]


def escaped(value):
    return ''.join('!' + char.lower() if char.isupper() else char for char in value)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', default=str(ROOT / '.cache/toolchains/go/bin/go'))
    parser.add_argument('--check', action='store_true', help='Validate inputs and stage notices without changing the inventory.')
    args = parser.parse_args()
    report_file = ROOT / 'docs/desktop-dependencies.json'
    report = json.loads(report_file.read_text())
    notices_root = ROOT / 'desktop/notices'
    notice_manifest_file = notices_root / 'manifest.json'
    old_notices = json.loads(notice_manifest_file.read_text())
    resources = ROOT / '.cache/desktop-resources'
    electron = ROOT / 'desktop/node_modules/electron/dist'
    resource_manifest = json.loads((resources / 'resource-manifest.json').read_text())
    if resource_manifest['target'] != 'darwin-arm64':
        raise RuntimeError('This refresh uses the existing macOS arm64 native audit only.')
    current_resources = inventory(resources)
    old_native = [{k: v for k, v in r.items() if k != 'component'}
                  for r in report['resource_files'] if r['path'].startswith('postgres/')]
    new_native = [r for r in current_resources if r['path'].startswith('postgres/')]
    if old_native != new_native or inventory(electron) != report['electron_files']:
        raise RuntimeError('Audited native/Electron files changed; perform a new native dependency audit.')
    components = {r['path']: r.get('component') for r in report['resource_files']}
    for item in current_resources:
        if components.get(item['path']):
            item['component'] = components[item['path']]
    if digest(resources / 'bin/overkill') != resource_manifest['backendSHA256']:
        raise RuntimeError('Prepared backend differs from resource manifest.')
    cache = Path(subprocess.check_output([args.go, 'env', 'GOMODCACHE'], cwd=ROOT, text=True).strip())
    build = subprocess.check_output([args.go, 'version', '-m', str(resources / 'bin/overkill')], cwd=ROOT, text=True)
    old_version = report['go_runtime']['version']
    if not re.search(r': go' + re.escape(old_version) + r'\s', build):
        raise RuntimeError('Go toolchain changed; refresh runtime notices before collecting modules.')
    modules = []
    collected = []
    # Stage every module before replacing this script-owned notice subtree.
    with tempfile.TemporaryDirectory(prefix='overkill-go-notices-', dir=notices_root.parent) as temporary:
        staging = Path(temporary) / 'go'
        staging.mkdir()
        for line in build.splitlines():
            fields = line.strip().split('\t')
            if fields[0] == '=>':
                raise RuntimeError('Replacement modules require explicit provenance review.')
            if fields[0] != 'dep':
                continue
            name, version, go_sum = fields[1:4]
            archive = cache / 'cache/download' / escaped(name) / '@v' / (version + '.zip')
            if archive.with_suffix('.ziphash').read_text().strip() != go_sum:
                raise RuntimeError('Cached Go checksum differs from binary: ' + name)
            module_records = []
            prefix = name + '@' + version + '/'
            with zipfile.ZipFile(archive) as source:
                for member in sorted(source.namelist()):
                    if member.endswith('/') or not member.startswith(prefix):
                        continue
                    relative = Path(member[len(prefix):])
                    if relative.is_absolute() or '..' in relative.parts:
                        raise RuntimeError('Unsafe module archive path: ' + member)
                    if not re.match(r'^(licen[cs]e|copying|notice|copyright|authors|patents)([._-].*)?$', relative.name, re.I):
                        continue
                    inert = Path(str(relative) + '.txt') if relative.suffix == '.go' else relative
                    local_relative = Path(name.replace('/', '__') + '@' + version) / inert
                    target = staging / local_relative
                    target.parent.mkdir(parents=True, exist_ok=True)
                    target.write_bytes(source.read(member))
                    final = notices_root / 'go' / local_relative
                    entry = {'component': name, 'path': final.relative_to(ROOT).as_posix(),
                             'source': 'Exact module ZIP ' + str(archive), 'archive_member': member,
                             'sha256': digest(target), 'bytes': target.stat().st_size,
                             'matches_module_archive': True}
                    module_records.append(entry)
                    collected.append({**record(target, staging), 'path': entry['path'],
                                      'provenance': {'url': 'https://proxy.golang.org/' + escaped(name) + '/@v/' + version + '.zip',
                                                     'archive_sha256': digest(archive), 'archive_member': member}})
            if not module_records:
                raise RuntimeError('No exact source notice found for module: ' + name)
            root_text = '\n'.join((staging / Path(r['path']).relative_to('desktop/notices/go')).read_text(errors='replace')[:50000]
                                  for r in module_records if len(Path(r['path']).relative_to('desktop/notices/go').parts) == 2)
            label = ('GPL-3.0-family' if 'GNU GENERAL PUBLIC LICENSE' in root_text else
                     'MPL-2.0' if 'Mozilla Public License Version 2.0' in root_text else
                     'Apache-2.0' if 'Apache License' in root_text else
                     'MIT' if 'Permission is hereby granted, free of charge' in root_text else
                     'BSD-family' if 'Redistribution and use in source and binary forms' in root_text else 'See exact license texts')
            info = archive.with_suffix('.info')
            modules.append({'module': name, 'version': version, 'go_sum': go_sum,
                            'module_zip_sha256': digest(archive),
                            'source_archive': 'https://proxy.golang.org/' + escaped(name) + '/@v/' + version + '.zip',
                            'origin_metadata': json.loads(info.read_text()) if info.exists() else {},
                            'license_observation': label, 'notice_files': module_records, 'gap': None,
                            'cache_go_sum_matches_binary': True})
        if not modules:
            raise RuntimeError('Backend build metadata contains no modules.')
        retained = [r for r in old_notices['files'] if not r['path'].startswith('desktop/notices/go/')]
        for item in retained:
            if digest(ROOT / item['path']) != item['sha256']:
                raise RuntimeError('Existing non-Go notice changed: ' + item['path'])
        # The research inventory is retained as evidence; no source archive is executed.
        audit = json.loads(subprocess.check_output(['/usr/bin/python3', str(ROOT / 'scripts/audit-desktop-resources.py'), str(resources)], text=True))
        if audit['errors']:
            raise RuntimeError('Prepared resource loader audit failed.')
        symbols = subprocess.check_output([args.go, 'tool', 'nm', str(resources / 'bin/overkill')], text=True)
        linked = '\n'.join(line for line in symbols.splitlines() if 'go.mau.fi/libsignal' in line) + '\n'
        if args.check:
            print(json.dumps({'go_modules': len(modules), 'notice_files': len(retained) + len(collected),
                              'native_inputs_unchanged': True, 'check_only': True}))
            return
        shutil.rmtree(notices_root / 'go')
        shutil.copytree(staging, notices_root / 'go')
    timestamp = datetime.datetime.now(datetime.timezone.utc).isoformat()
    records = sorted(retained + collected, key=lambda item: item['path'])
    report.update(generated_at=timestamp, distribution_ready=False, go_modules=modules,
                  go_notice_archive_mismatches=[], notice_files=records, resource_files=current_resources)
    report['refresh'] = {'tool': 'scripts/refresh-desktop-go-notices.py', 'at': timestamp,
                         'scope': 'Prepared backend module/notice/hash refresh; prior native and Electron snapshots verified unchanged.',
                         'notice_archive_bytes_verified': True, 'native_inputs_unchanged': True}
    report['snapshot'].update(resource_manifest=resource_manifest, backend_sha256=digest(resources / 'bin/overkill'),
                              backend_manifest_matches=True, resource_file_count=len(current_resources),
                              resource_macho_count=audit['macho_files'], resource_audit_errors=audit['errors'])
    report['coverage']['inspected'] = [re.sub(r'All\s*\d+ embedded\s*Go modules', f'All {len(modules)} embedded Go modules', value)
                                          for value in report['coverage']['inspected']]
    evidence = ROOT / report['linked_copyleft_symbol_evidence']['path']
    evidence.parent.mkdir(parents=True, exist_ok=True)
    evidence.write_text(linked)
    report['linked_copyleft_symbol_evidence'].update(sha256=digest(evidence), matched_symbols=len(linked.splitlines()))
    report_file.write_text(json.dumps(report, indent=2) + '\n')
    old_notices.update(generated_at=timestamp, distribution_ready=False, files=records)
    notice_manifest_file.write_text(json.dumps(old_notices, indent=2) + '\n')
    documentation = ROOT / 'docs/desktop-dependencies.md'
    text = documentation.read_text()
    text = re.sub(r'All \*\*\d+ modules embedded', f'All **{len(modules)} modules embedded', text)
    text = re.sub(r'and \d+ dependency modules', f'and {len(modules)} dependency modules', text)
    text = re.sub(r'\*\*\d+ copied notice/source-evidence files\*\*', f'**{len(records)} copied notice/source-evidence files**', text)
    text = re.sub(r'\*\*\d+ matching libsignal symbols\*\*', f'**{len(linked.splitlines())} matching libsignal symbols**', text)
    documentation.write_text(text)
    print(json.dumps({'backend_sha256': report['snapshot']['backend_sha256'], 'go_modules': len(modules),
                      'notice_files': len(records), 'native_inputs_unchanged': True,
                      'go_notice_archive_mismatches': [], 'distribution_ready': False}))


if __name__ == '__main__':
    main()
