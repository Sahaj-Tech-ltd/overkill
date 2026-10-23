# Archive safety fixture

`sparse-logical-limit.tgz` is a controlled GNU sparse PAX archive generated for the aggregate-expansion regression. It contains three `root/file{i}` regular entries with one physical byte `x` each and PAX records `GNU.sparse.map=0,1`, `GNU.sparse.size=3000`, `GNU.sparse.numblocks=1`. Python tarfile PAX_FORMAT output was trimmed to 7,168 physical tar bytes and gzip-compressed with mtime=0 (212 bytes). Go archive/tar synthesizes 9,000 logical output bytes, so an 8,000-byte expansion budget must reject it. No third-party module content is included.
