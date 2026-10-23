#!/usr/bin/env python3
"""Build a traceable discovery index; this does not infer feature parity."""
import collections
import hashlib
import json
import os
import tempfile
from harness_normalization import PARITY, resolve_evidence_path, validate_normalization
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
REPORTS = ROOT / "docs/harness-research"
SLUGS = ("opencode", "codex", "pi", "cline", "hermes-agent", "openclaw", "factory", "warp")
# Reviewed mapping packages. Additional reports are integrated only after review.
ACCEPTED_PARITY = {"factory": None, "warp": None, "opencode": "capability.", "codex": "capability.", "pi": "capability."}
ALIASES = {
    "runtime/providers": "runtime/model/provider", "runtime": "runtime/model/provider", "providers": "runtime/model/provider",
    "context": "context/memory", "tools": "tools/edit/exec/search/browser",
    "sandbox/permissions": "permissions/sandbox", "security": "permissions/sandbox",
    "plugins": "skills/plugins/MCP/hooks", "MCP": "skills/plugins/MCP/hooks", "hooks": "skills/plugins/MCP/hooks",
    "agents": "agents/delegation/messaging", "messaging": "agents/delegation/messaging",
    "planning/review/workflows": "workflows/planning/review", "planning": "workflows/planning/review", "review": "workflows/planning/review",
    "sessions": "sessions/recovery", "IDE/remote/headless/integrations": "integrations/IDE/remote/headless",
    "IDE": "integrations/IDE/remote/headless", "remote": "integrations/IDE/remote/headless",
    "UX": "frontend/UX", "distribution": "distribution/updates", "cost": "observability/cost/evals", "evals": "observability/cost/evals",
}


def record_type(identifier):
    for prefix, kind in (("section:", "documentation_section"), ("docs:", "documentation_page"),
                         ("cli.", "cli_surface"), ("config.", "configuration_field"),
                         ("feature-flag.", "feature_flag"), ("api.", "api_surface"),
                         ("rpc.", "api_surface"), ("hook.", "lifecycle_hook"),
                         ("tool.", "tool"), ("tool:", "tool")):
        if identifier.startswith(prefix):
            return kind
    return "curated_capability"



def apply_original_assessment(feature, entry, root, errors, stale_anchors):
    """Read an explicit original-record comparison, never infer from children."""
    assessment = feature.get("overkill")
    if assessment is None:
        return
    context = f"{entry['harness']}/{entry['id']} original assessment"
    if not isinstance(assessment, dict) or assessment.get("status") not in PARITY or not assessment.get("reason"):
        errors.append(f"{context}: explicit parity status/reason required")
        return
    anchors = assessment.get("production_evidence", [])
    if not isinstance(anchors, list) or (assessment["status"] == "verified" and not anchors):
        errors.append(f"{context}: invalid or missing production evidence")
        return
    stale = False
    for item in anchors:
        if not isinstance(item, dict):
            errors.append(f"{context}: invalid evidence object")
            continue
        local = resolve_evidence_path(root, entry["harness"], item.get("path"), production=True)
        if local is None:
            errors.append(f"{context}: missing contained production evidence {item.get('path')}")
            continue
        data = local.read_bytes()
        lines = data.decode(errors="replace").splitlines(keepends=True)
        start, end = item.get("line"), item.get("end_line")
        if not isinstance(start, int) or not isinstance(end, int) or not 1 <= start <= end <= len(lines):
            errors.append(f"{context}: invalid evidence span")
            continue
        if not item.get("sha256") or not item.get("excerpt_sha256"):
            errors.append(f"{context}: file and excerpt hashes required")
        elif hashlib.sha256(data).hexdigest() != item["sha256"] or hashlib.sha256("".join(lines[start-1:end]).encode()).hexdigest() != item["excerpt_sha256"]:
            stale = True
            stale_anchors.add(item["path"])
    entry.update(overkill_status="unknown" if stale and assessment["status"] == "verified" else assessment["status"],
                 parity_report=entry["report"], parity_evidence_status="source-changed-review-required" if stale else "snapshot-current")
    if stale and assessment["status"] == "verified":
        entry["previous_assessment"] = "verified"


def main():
    entries, harnesses, errors = [], [], []
    line_counts = {}
    parity_counts = collections.Counter()
    stale_anchors = set()
    for slug in SLUGS:
        report = json.loads((REPORTS / f"{slug}.json").read_text())
        parity = {}
        if slug in ACCEPTED_PARITY:
            mapping = json.loads((REPORTS / f"{slug}-parity.json").read_text())
            for record in mapping["records"]:
                identifier = record.get("feature_id", record.get("id"))
                assessment = record.get("overkill", record)
                if identifier in parity:
                    errors.append(f"{slug}: duplicate parity ID {identifier}")
                if not assessment.get("reason"):
                    errors.append(f"{slug}/{identifier}: parity reason missing")
                parity[identifier] = assessment
            prefix = ACCEPTED_PARITY[slug]
            originals = {f["id"] for f in report["features"] if prefix is None or f["id"].startswith(prefix)}
            if set(parity) != originals:
                errors.append(f"{slug}: parity ID coverage differs from original report")
        seen = set()
        for feature in report["features"]:
            identifier = feature["id"]
            if identifier in seen:
                errors.append(f"{slug}: duplicate ID {identifier}")
            seen.add(identifier)
            if not feature.get("evidence"):
                errors.append(f"{slug}/{identifier}: no evidence")
            for evidence in feature.get("evidence", []):
                if not evidence.get("url", "").startswith("https://"):
                    errors.append(f"{slug}/{identifier}: missing HTTPS evidence URL")
                path = evidence.get("path")
                if not path:
                    continue
                local = resolve_evidence_path(ROOT, slug, path)
                if local is None:
                    errors.append(f"{slug}/{identifier}: missing local evidence {path}")
                    continue
                if local not in line_counts:
                    line_counts[local] = len(local.read_text(errors="replace").splitlines())
                line = evidence.get("line")
                if line is not None and not 1 <= line <= line_counts[local]:
                    errors.append(f"{slug}/{identifier}: invalid evidence line {line}: {path}")
            entry = {"harness": slug, "id": identifier, "name": feature["name"],
                            "record_type": record_type(identifier),
                            "category": ALIASES.get(feature["category"], feature["category"]),
                            "source_category": feature["category"], "source_status": feature["status"],
                            "report": f"{slug}.json", "overkill_status": "unassessed"}
            if identifier in parity:
                assessment = parity[identifier]
                stale = False
                for evidence in assessment.get("production_evidence", assessment.get("overkill_evidence", [])):
                    if not evidence.get("path"):
                        continue
                    local = ROOT / evidence["path"]
                    if not local.is_file():
                        errors.append(f"{slug}/{identifier}: parity evidence missing {local}")
                        continue
                    if evidence.get("sha256") and hashlib.sha256(local.read_bytes()).hexdigest() != evidence["sha256"]:
                        stale = True
                        stale_anchors.add(evidence["path"])
                entry.update(overkill_status=assessment["status"], parity_report=f"{slug}-parity.json",
                             parity_evidence_status="source-changed-review-required" if stale else "snapshot-current")
                if stale and assessment["status"] == "verified":
                    entry.update(overkill_status="unknown", previous_assessment="verified")
            else:
                apply_original_assessment(feature, entry, ROOT, errors, stale_anchors)
            parity_counts[entry["overkill_status"]] += 1
            entries.append(entry)
        own = [entry for entry in entries if entry["harness"] == slug]
        harnesses.append({"slug": slug, "name": report["name"], "source": report["source"],
                          "records": len(own), "record_types": dict(collections.Counter(e["record_type"] for e in own)),
                          "source_statuses": dict(collections.Counter(e["source_status"] for e in own)),
                          "deepwiki_status": report["deepwiki"]["status"]})
    normalized = []
    for slug in ("cline", "hermes-agent", "openclaw"):
        mapping = json.loads((REPORTS / f"{slug}-capabilities.json").read_text())
        original = json.loads((REPORTS / f"{slug}.json").read_text())
        original_ids = {record["id"] for record in original["features"]}
        coverage = mapping["coverage"]
        covered = set(coverage["mapped_discovery_record_ids"])
        remaining = set(coverage["remaining_uninspected_discovery_record_ids"])
        if covered & remaining or covered | remaining != original_ids:
            errors.append(f"{slug}: normalized coverage partition differs from original report")
        if len(mapping["category_counts"]) != 12:
            errors.append(f"{slug}: normalized categories incomplete")
        seen = set()
        for record in mapping["capabilities"]:
            identifier = record["id"]
            if identifier in seen:
                errors.append(f"{slug}: duplicate normalized ID {identifier}")
            seen.add(identifier)
            if not set(record["upstream"]["source_record_ids"]) <= original_ids:
                errors.append(f"{slug}/{identifier}: unknown source record ID")
            assessment = record["overkill"]
            if not assessment.get("reason") or not record["upstream"].get("evidence"):
                errors.append(f"{slug}/{identifier}: missing normalization provenance")
            stale = False
            for evidence in assessment.get("production_evidence", []):
                local = ROOT / evidence["path"]
                if not local.is_file():
                    errors.append(f"{slug}/{identifier}: missing parity source {local}")
                elif evidence.get("sha256") and hashlib.sha256(local.read_bytes()).hexdigest() != evidence["sha256"]:
                    stale = True
                    stale_anchors.add(evidence["path"])
            normalized.append({"harness": slug, "id": identifier, "name": record["name"],
                               "category": ALIASES.get(record["category"], record["category"]),
                               "upstream_status": record["upstream"]["status"],
                               "overkill_status": "unknown" if stale and assessment["status"] == "verified" else assessment["status"],
                               "source_record_ids": record["upstream"]["source_record_ids"],
                               "parity_report": f"{slug}-capabilities.json",
                               "parity_evidence_status": "source-changed-review-required" if stale else "snapshot-current"})
    complete_normalizations = []
    discovery_index = {(entry["harness"], entry["id"]): entry for entry in entries}
    for slug in SLUGS:
        path = REPORTS / f"{slug}-normalized.json"
        if not path.is_file():
            continue
        mapping = json.loads(path.read_text())
        original = json.loads((REPORTS / f"{slug}.json").read_text())
        summary, capabilities = validate_normalization(ROOT, slug, mapping, original, errors, line_counts, stale_anchors)
        complete_normalizations.append(summary)
        normalized.extend(capabilities)
        for disposition in mapping.get("raw_record_dispositions", []):
            entry = discovery_index.get((slug, disposition.get("source_record_id")))
            if entry is not None:
                entry["normalization"] = {"report": f"{slug}-normalized.json",
                                          "disposition": disposition.get("disposition"),
                                          "capability_ids": disposition.get("capability_ids", [])}
                # Do not promote raw-record parity from a broader capability.

    output = {"schema_version": 1,
              "method": "Discovery index referencing original evidence records; no fuzzy deduplication or inferred parity. Static implemented labels are source observations, not runtime certification. Categories retain the researchers' classification and are not parity scores.",
              "limitation": "Documentation sections, pages, flags, settings and capabilities are different record types; their total is not a unique feature count. Only accepted parity packages and explicit original-record comparisons have mappings; other records remain unassessed. Source hashes detect subsequent changes but do not verify semantic equivalence.",
              "harnesses": harnesses, "entries": entries,
              "normalized_capabilities": normalized,
              "complete_normalization_reports": complete_normalizations,
              "normalization_scope": "Additional semantic capability mappings are not added to discovery counts or treated as verification of every heading on their source pages. Coverage partitions and remaining work are in the source reports.",
              "validation": {"records": len(entries), "local_evidence_files_checked": len(line_counts),
                             "additional_normalized_capabilities": len(normalized),
                             "parity_status_counts": dict(parity_counts), "changed_parity_source_paths": sorted(stale_anchors), "errors": errors}}
    print(json.dumps(output["validation"], indent=2))
    if errors:
        raise SystemExit(1)  # Preserve the last valid catalogue on bad input.
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode="w", dir=REPORTS, prefix=".catalogue-", suffix=".tmp", delete=False) as stream:
            temporary = stream.name
            stream.write(json.dumps(output, indent=2) + "\n")
        os.replace(temporary, REPORTS / "catalogue.json")
    finally:
        if temporary and os.path.exists(temporary):
            os.unlink(temporary)


if __name__ == "__main__":
    main()
