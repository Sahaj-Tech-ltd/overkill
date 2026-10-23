Automatic memory is disabled by default. Enable provider-backed extraction explicitly in config.toml:

```toml
[auto_memory]
enabled = true
min_new_messages = 5
```

The threshold counts newly admitted user messages since the last successful extraction. After a successful normal or streaming turn, the configured session provider receives a separate, tool-free request containing up to twenty recent user messages, bounded to 24 KiB. Assistant text, tool results, attachments, system prompts and credentials from provider configuration are excluded. Common credential/personal-identifier patterns are redacted before submission; this heuristic cannot recognize every sensitive statement, so opt in only when this additional provider processing is appropriate.

Extraction accepts only strict JSON, up to twelve facts, with `user`, `project` or `general` categories and single-line content up to 500 bytes. Credential-like text and obvious override instructions are rejected; the entire batch is rejected if one fact is invalid. Provider errors/payloads and transcripts are never persisted as memory. The provider and model are those chosen when the session is constructed.

The store is `<ConfigDir>/memory/<canonical workspace hash>/facts.json`, not a second `.overkill` below ConfigDir. Workspace scoping prevents project facts being recalled in unrelated folders. Atomic replacement, private file permissions and cross-process file locking protect writes; duplicate content is compared case-insensitively after whitespace normalization. At most 200 facts are retained; each prompt reloads up to twenty recent facts as explicitly untrusted reference data, never executable instructions or permission. Legacy daily Markdown files are not imported automatically.

Existing user.yaml controls apply on each extraction and reload:

```yaml
advanced:
  memory:
    enabled: false
    max_entries: 100
    ttl_days: 30
```

`enabled: false` blocks both extraction and recall, including existing live extractors. `max_entries` lowers the hard 200-entry ceiling; positive `ttl_days` excludes expired entries. Expired entries are removed on the next successful write. `embedding_model` does not apply to this file-based fact store. Re-enable/reconfigure a session constructed while disabled by creating a new session.

Extraction is best effort and bounded to fifteen seconds, tied to the turn and session cancellation. It checks active goal limits before requesting and reports returned usage through the agent’s goal accounting and usage observer before publishing facts. It completes before a successful turn returns, allowing a one-shot CLI to save before shutdown. An extraction error leaves the completed answer intact and does not publish an invalid/partial fact batch. No paid provider is used by package/runtime acceptance tests.
