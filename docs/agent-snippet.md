## OpenAPI specs

Never read an OpenAPI file whole or page through it. Use `oq` (addresses nodes, prints exact text):

- `oq ls SPEC ops` / `oq ls SPEC schemas`: find things; the last column is ~tokens.
- `oq outline SPEC [ADDR]`: children of a node with sizes. Unfold step by step.
- `oq get SPEC ADDR`: the exact text of one node. ADDR is `/json/pointer`, `GET /path`, `path:/path` (all methods), `opid:name` or `schema:Name`.
- `oq deps SPEC ADDR --depth 1`: what a node references; `oq refs SPEC ADDR`: what references it.
- `oq at SPEC LINE`: which node a line belongs to (use with grep -n and linter output).
- Exit 4 means the answer was over the size cap: narrow the address or use `outline`. Do not retry with `--max-tokens 0`.
- To edit, `oq get SPEC ADDR -o columns=hash` first and keep the hash, then
  `oq set SPEC ADDR --expect HASH -i < new.yaml` (also `insert`, `rm`). Exit 3 means the node changed: re-read it.
  Never hand-edit a large spec file.
