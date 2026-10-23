#!/usr/bin/env python3
"""Validate the complete CLI publication set without publishing anything."""
import hashlib
import re
import sys
from pathlib import Path

ASSETS = (
    'overkill-linux-amd64', 'overkill-linux-arm64',
    'overkill-darwin-amd64', 'overkill-darwin-arm64',
    'overkill-windows-amd64.exe',
)


def verify(directory):
    directory = Path(directory)
    expected = set(ASSETS) | {'checksums.txt'}
    actual = {entry.name for entry in directory.iterdir()}
    if actual != expected:
        raise ValueError(f'invalid release assets: missing={sorted(expected-actual)}, unexpected={sorted(actual-expected)}')
    for name in expected:
        path = directory / name
        if path.is_symlink() or not path.is_file():
            raise ValueError(f'release asset must be a regular file: {name}')
    checksums = {}
    for line in (directory / 'checksums.txt').read_text(encoding='utf-8').splitlines():
        match = re.fullmatch(r'([0-9a-fA-F]{64})  (overkill-[A-Za-z0-9.-]+)', line)
        if not match:
            raise ValueError('malformed checksum manifest entry')
        digest, name = match.groups()
        if name not in ASSETS or name in checksums:
            raise ValueError(f'unexpected or duplicate checksum entry: {name}')
        checksums[name] = digest.lower()
    if set(checksums) != set(ASSETS):
        raise ValueError('checksum manifest must contain every release target exactly once')
    for name in ASSETS:
        digest = hashlib.sha256()
        with (directory / name).open('rb') as asset:
            for block in iter(lambda: asset.read(1024 * 1024), b''):
                digest.update(block)
        if digest.hexdigest() != checksums[name]:
            raise ValueError(f'SHA256 mismatch for {name}')
    return ASSETS


if __name__ == '__main__':
    try:
        verified = verify(sys.argv[1] if len(sys.argv) == 2 else 'dist')
    except (OSError, UnicodeError, ValueError) as error:
        sys.exit(f'release verification failed: {error}')
    print(f'Verified {len(verified)} release assets and mandatory checksums.')
