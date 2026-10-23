"""Structural/provenance checks for complete discovery-record dispositions.

This validates references and coverage, not the truth of authored semantics or
runtime parity. Missing/unresolved behavior remains explicit in the output.
"""
import collections
import hashlib
from pathlib import Path
from urllib.parse import urlparse

DISPOSITIONS = {'capability', 'variant', 'duplicate', 'implementation_detail', 'nonfeature', 'unresolved'}
PARITY = {'verified', 'partial', 'present-but-unwired', 'missing', 'unknown', 'unassessed', 'not-applicable'}


def resolve_evidence_path(root, slug, path, production=False):
    """Resolve declared ownership, never select a repository by file existence.

    Upstream-relative paths belong to that harness snapshot. Explicit collected
    paths under research/harnesses and production paths belong to the workspace.
    Neither a missing snapshot nor an escaping symlink may fall back to another
    repository's file of the same name.
    """
    root = Path(root).resolve()
    if not isinstance(path, str) or not path:
        return None
    relative = Path(path)
    if '..' in relative.parts:
        return None
    explicit = relative.is_absolute() or relative.parts[:2] == ('research', 'harnesses')
    base = root if production or explicit else root / 'research/harnesses' / slug / 'source'
    owner = base.resolve()
    local = (base / relative).resolve()
    if local.is_relative_to(root) and local.is_relative_to(owner) and local.is_file():
        return local
    return None


def validate_normalization(root, slug, mapping, original, errors, line_counts, stale_anchors):
    root = Path(root).resolve()
    label = f'{slug} complete normalization'
    if mapping.get('schema_version') != 1 or mapping.get('harness') != slug:
        errors.append(f'{label}: unsupported schema/harness')
    if mapping.get('source', {}).get('commit') != original['source'].get('commit'):
        errors.append(f'{label}: source commit differs from discovery snapshot')
    originals = {record['id'] for record in original['features']}
    source_lines, source_hashes = {}, {}

    def evidence(records, context, production=False, require_url=True):
        stale = False
        if not isinstance(records, list) or not records:
            errors.append(f'{context}: evidence required')
            return False
        for item in records:
            if not isinstance(item, dict):
                errors.append(f'{context}: invalid evidence object')
                continue
            if not production and require_url:
                parsed = urlparse(item.get('url', ''))
                if parsed.scheme != 'https' or not parsed.netloc:
                    errors.append(f'{context}: HTTPS source URL required')
            path = item.get('path')
            if not path:
                if production:
                    errors.append(f'{context}: production path required')
                continue
            local = resolve_evidence_path(root, slug, path, production=production)
            if local is None:
                errors.append(f'{context}: missing/escaping evidence path {path}')
                continue
            if local not in line_counts:
                line_counts[local] = len(local.read_text(errors='replace').splitlines())
            line = item.get('line')
            if line is not None and (type(line) is not int or not 1 <= line <= line_counts[local]):
                errors.append(f'{context}: invalid line {line}: {path}')
            if 'end_line' in item or 'inspection_start_line' in item:
                start, end = item.get('inspection_start_line', line), item.get('end_line')
                valid_span = type(start) is int and type(end) is int and 1 <= start <= end <= line_counts[local]
                if not valid_span:
                    errors.append(f'{context}: invalid inspection span {start}..{end}: {path}')
                elif 'excerpt' in item and not item.get('excerpt_truncated'):
                    if local not in source_lines:
                        source_lines[local] = local.read_text(errors='replace').splitlines()
                    actual = '\n'.join(source_lines[local][start - 1:end]).strip()
                    if not isinstance(item['excerpt'], str) or actual != item['excerpt'].strip():
                        errors.append(f'{context}: excerpt differs from inspected source span: {path}:{start}..{end}')
            if item.get('sha256') and local not in source_hashes:
                source_hashes[local] = hashlib.sha256(local.read_bytes()).hexdigest()
            if item.get('sha256') and source_hashes[local] != item['sha256']:
                if production:
                    stale = True
                    stale_anchors.add(path)
                else:
                    errors.append(f'{context}: upstream snapshot hash changed: {path}')
        return stale

    records = mapping.get('capabilities', [])
    capabilities = {}
    normalized = []
    for record in records:
        identifier = record.get('id')
        context = f'{slug}/{identifier}'
        if not identifier or identifier in capabilities:
            errors.append(f'{label}: empty/duplicate capability ID {identifier}')
            continue
        capabilities[identifier] = record
        if not record.get('name') or not record.get('category'):
            errors.append(f'{context}: name/category required')
        upstream = record.get('upstream', {})
        ids = upstream.get('source_record_ids', [])
        if not isinstance(ids, list) or len(ids) != len(set(ids)) or not set(ids) <= originals:
            errors.append(f'{context}: invalid source record IDs')
        if not upstream.get('status'):
            errors.append(f'{context}: upstream status required')
        evidence(upstream.get('evidence'), context)
        assessment = record.get('overkill', {})
        status = assessment.get('status')
        if status not in PARITY or not assessment.get('reason'):
            errors.append(f'{context}: explicit parity status/reason required')
        anchors = assessment.get('production_evidence', [])
        stale = evidence(anchors, context, production=True) if anchors else False
        if status == 'verified' and not anchors:
            errors.append(f'{context}: verified assessment lacks production anchors')
        normalized.append({'harness': slug, 'id': identifier, 'name': record.get('name'),
                           'category': record.get('category'), 'upstream_status': upstream.get('status'),
                           'overkill_status': 'unknown' if stale and status == 'verified' else status,
                           'source_record_ids': ids, 'parity_report': f'{slug}-normalized.json',
                           'source_record_roles': upstream.get('source_record_roles', {}),
                           'source_discovery_ids': upstream.get('source_discovery_ids', []),
                           'parity_evidence_status': 'source-changed-review-required' if stale else 'snapshot-current'})

    dispositions = mapping.get('raw_record_dispositions', [])
    seen, counts = set(), collections.Counter()
    references = collections.defaultdict(set)
    for record in dispositions:
        identifier = record.get('source_record_id')
        context = f'{label}/{identifier}'
        if identifier not in originals or identifier in seen:
            errors.append(f'{context}: unknown/duplicate discovery ID')
        seen.add(identifier)
        disposition = record.get('disposition')
        counts[disposition] += 1
        if disposition not in DISPOSITIONS or not record.get('reason'):
            errors.append(f'{context}: explicit disposition/reason required')
        ids = record.get('capability_ids', [])
        if not isinstance(ids, list) or len(ids) != len(set(ids)):
            errors.append(f'{context}: capability references must be a unique list')
            ids = []
        if disposition in {'capability', 'variant', 'duplicate'} and not ids:
            errors.append(f'{context}: behavioral disposition has no capability')
        for capability_id in ids:
            if capability_id not in capabilities:
                errors.append(f'{context}: missing capability {capability_id}')
            references[capability_id].add(identifier)
        evidence(record.get('evidence'), context)
    if seen != originals or len(dispositions) != len(originals):
        errors.append(f'{label}: disposition partition must cover every original ID exactly once')
    for identifier, record in capabilities.items():
        declared = set(record.get('upstream', {}).get('source_record_ids', []))
        if references[identifier] != declared:
            errors.append(f'{slug}/{identifier}: source IDs disagree with disposition backlinks')
    # Supplemental discoveries remain separate from the immutable original-ID
    # partition. Contextual umbrella references cannot turn them into original
    # discoveries or establish behavior by themselves.
    discoveries = mapping.get('source_discoveries', [])
    if not isinstance(discoveries, list):
        errors.append(f'{label}: source_discoveries must be a list')
        discoveries = []
    discovered, discovery_references = {}, collections.defaultdict(set)
    for record in discoveries:
        if not isinstance(record, dict):
            errors.append(f'{label}: invalid supplemental discovery object')
            continue
        identifier = record.get('id')
        context = f'{label}/discovery/{identifier}'
        if not isinstance(identifier, str) or not identifier or identifier in discovered:
            errors.append(f'{context}: empty/duplicate discovery ID')
            continue
        discovered[identifier] = record
        if not record.get('kind') or not record.get('status'):
            errors.append(f'{context}: kind/status required')
        if not record.get('path') or type(record.get('line')) is not int:
            errors.append(f'{context}: exact source path/line required')
        evidence([record], context, require_url=False)
        ids = record.get('capability_ids', [])
        if not isinstance(ids, list) or len(ids) != len(set(ids)):
            errors.append(f'{context}: capability references must be a unique list')
            ids = []
        if not ids and not record.get('reason'):
            errors.append(f'{context}: unmapped discovery requires an explicit reason')
        for capability_id in ids:
            if capability_id not in capabilities:
                errors.append(f'{context}: missing capability {capability_id}')
            discovery_references[capability_id].add(identifier)
    for identifier, record in capabilities.items():
        upstream = record.get('upstream', {})
        ids = upstream.get('source_discovery_ids', [])
        if not isinstance(ids, list) or len(ids) != len(set(ids)) or not set(ids) <= discovered.keys():
            errors.append(f'{slug}/{identifier}: invalid supplemental discovery IDs')
            ids = []
        if set(ids) != discovery_references[identifier]:
            errors.append(f'{slug}/{identifier}: supplemental IDs disagree with discovery backlinks')
    summary = {'harness': slug, 'report': f'{slug}-normalized.json',
               'discovery_records': len(originals), 'disposition_records': len(dispositions),
               'semantic_capabilities_and_variants': len(capabilities),
               'disposition_counts': dict(counts),
               'original_id_partition_complete': seen == originals and len(dispositions) == len(originals),
               'unresolved_records': counts['unresolved'],
               'supplemental_source_discoveries': len(discovered),
               'limitation': 'Complete raw-ID disposition is not complete feature discovery, semantic correctness or verified runtime parity. Source surfaces outside the original inventory and unresolved records remain scoped in the report.'}
    return summary, normalized
