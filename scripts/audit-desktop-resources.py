#!/usr/bin/env python3
"""Inventory a prepared macOS runtime without changing it.

Checksums and loader dependencies are evidence, not license clearance or a
substitute for launching the installed app. Run after prepare-desktop.mjs.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess

MACHO = {bytes.fromhex(value) for value in (
    'feedface', 'cefaedfe', 'feedfacf', 'cffaedfe', 'cafebabe', 'bebafeca', 'cafebabf', 'bfbafeca')}


def inspect_binary(path):
    result = subprocess.run(['/usr/bin/dyld_info', '-dependents', '-platform', str(path)],
                            capture_output=True, text=True, timeout=15, check=True)
    architectures = {}
    current = None
    section = None
    for line in result.stdout.splitlines():
        header = re.search(r'\[([^]]+)\]:$', line)
        if header:
            current = architectures.setdefault(header[1], {'dependencies': [], 'rpaths': [], 'platforms': []})
            section = None
            continue
        if current is None:
            continue
        stripped = line.strip()
        if stripped.startswith('-') and stripped.endswith(':'):
            section = stripped
        elif section == '-linked_dylibs:':
            parts = stripped.split()
            if parts and (parts[-1].startswith(('@', '/'))):
                current['dependencies'].append(parts[-1])
        elif section == '-rpaths:' and stripped.startswith(('@', '/')):
            current['rpaths'].append(stripped)
        elif section == '-platform:':
            parts = stripped.split()
            if len(parts) == 3 and parts[0] == 'macOS':
                current['platforms'].append({'name': parts[0], 'minimum': parts[1], 'sdk': parts[2]})
    if not architectures:
        raise ValueError(f'No Mach-O architecture metadata: {path}')
    return architectures


def inventory(root):
    root = root.resolve(strict=True)
    records, errors = [], []
    for path in sorted(root.rglob('*')):
        relative = str(path.relative_to(root))
        if path.is_symlink():
            resolved = path.resolve()
            if not resolved.is_relative_to(root) or not resolved.exists():
                errors.append(f'Broken or escaping symlink: {relative}')
            records.append({'path': relative, 'symlink': str(path.readlink())})
            continue
        if not path.is_file():
            continue
        digest = hashlib.sha256()
        with path.open('rb') as stream:
            magic = stream.read(4)
            digest.update(magic)
            for block in iter(lambda: stream.read(1024 * 1024), b''):
                digest.update(block)
        record = {'path': relative, 'bytes': path.stat().st_size, 'sha256': digest.hexdigest()}
        if magic in MACHO:
            try:
                record['macho'] = inspect_binary(path)
                if 'arm64' not in record['macho']:
                    errors.append(f'Missing arm64 slice: {relative}')
                for arch, metadata in record['macho'].items():
                    for dependency in metadata['dependencies']:
                        if dependency.startswith(('/usr/lib/', '/System/Library/')):
                            continue  # dyld shared-cache paths need not exist on disk.
                        def expand(value):
                            return value.replace('@loader_path', str(path.parent))
                        if dependency.startswith('@rpath/'):
                            candidates = [Path(expand(base)) / dependency[len('@rpath/'):]
                                          for base in metadata['rpaths']]
                        elif dependency.startswith('@loader_path/'):
                            candidates = [Path(expand(dependency))]
                        else:
                            candidates = []
                        if not any(candidate.resolve().is_relative_to(root) and candidate.exists()
                                   for candidate in candidates):
                            errors.append(f'Unresolved non-system dependency: {relative} [{arch}] {dependency}')
            except (ValueError, subprocess.SubprocessError) as error:
                errors.append(f'{relative}: {error}')
        records.append(record)
    return {'schema': 1, 'target': 'darwin-arm64', 'files': records,
            'regular_files': sum('sha256' in record for record in records),
            'macho_files': sum('macho' in record for record in records), 'errors': errors,
            'license_review': 'pending: vendor archive omits dependency license texts; inventory is not redistribution clearance',
            'distribution_ready': False}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('resources', type=Path)
    arguments = parser.parse_args()
    result = inventory(arguments.resources)
    print(json.dumps(result, indent=2))
    raise SystemExit(bool(result['errors']))
