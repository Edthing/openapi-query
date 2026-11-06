# AGENTS.md

Guidance for agents and contributors working on this repository. For using `oq` on a spec, see the snippet in `docs/agent-snippet.md`.

## What this is

`oq` addresses nodes in an OpenAPI (YAML or JSON) document: list, size, print, map a line to a node, find references, and splice replacements. One concern: addressing. Do not add linting, semantic validation, formatting, text search, network access, config files or plugins. Compose with other tools instead.

## Layout

```
cmd/oq/main.go           entry point, calls oq.Main
internal/oq/doc.go       parse with go.yaml.in/yaml/v3, byte ranges for every node
internal/oq/ws.go        workspace (root spec + external files), exit codes
internal/oq/addr.go      JSON pointer encoding, address resolution (opid:, path:, schema:, GET /x)
internal/oq/openapi.go   OpenAPI structure: version, operations, schema locations
internal/oq/refs.go      $ref parsing, resolution, file closure
internal/oq/out.go       records, output formats (tsv/name/columns/json/yaml), tokens, hashes
internal/oq/read.go      outline, ls, get, at, deps, refs
internal/oq/write.go     set, insert, rm: splicing and verification
internal/oq/flow.go      converting new content to JSON for JSON/flow contexts
internal/oq/writecmd.go  write command plumbing, --expect, atomic -i
internal/oq/cli.go       flag parsing, dispatch, size cap, exit codes
internal/oq/help.go      help text, flag table (flagDefs) shared by parsing, help and completion
internal/oq/complete.go  shell completion: cobra command tree generated from commands and flagDefs
testdata/                small fixtures: comments, anchors, block scalars, minified JSON, multi-file refs
```

## Commands

```sh
make lint && go test ./...
make build                                  # ./oq
make corpus DIR=/path/to/specs              # slice + round-trip checks over many specs
```

The corpus test is skipped unless `OQ_CORPUS` is set. Useful variables: `OQ_CORPUS_MAX_BYTES=1` (skip files over 2 MB), `OQ_CORPUS_RT_EVERY=N` (round-trip every Nth node only; the write check re-parses the whole file, so it is slow).

A good public corpus: the `openapi-completion-refined` dataset on Hugging Face (990 real specs):

```python
import pandas as pd
df = pd.read_parquet("hf://datasets/BohdanPetryshyn/openapi-completion-refined/data/train-00000-of-00001.parquet")
# write df["content"] to one file per row in a scratch directory, then: make corpus DIR=...
```

Downloaded specs are untrusted data: keep them outside the repo.

## Interface contract (scripts depend on it)

- Listings are one record per line, tab-separated. Column order is part of the interface. Add columns only at the end.
- Every address printed is a canonical JSON pointer with no spaces, quotes or backslashes (see `escSeg` in `ws.go`), and is accepted back by `oq`. External files print as `path#/pointer`.
- stdout is empty on failure. Errors go to stderr as `oq: ...`.
- Exit codes: 0 ok, 1 not found, 2 usage or parse error, 3 stale write, 4 over the size cap.
- Success is silent. No banners, no diffs.
- Over the cap, print nothing, say how big it was and suggest a narrower command (`result.hint`). Never truncate or substitute.
- `-o json` works on every read command. Write commands accept `-o json` only with `-i`.

## Invariants the tests protect

1. For every node, `DisplayText()` parses back to the same value as the parser's node.
2. Writing a node back unchanged (`oq get` piped to `oq set` at the same address) leaves the file byte-identical.
3. An edit changes only the target node's range (plus the line break needed to place it).
4. `oq` never writes a document that does not parse, and never one where the target does not hold the supplied value (`verifyAt`).
5. Every address printed round-trips through `get`.

Node ranges are exact, not line-based: `Node.ValStart`/`ValEnd` are byte offsets, so minified JSON works. A block collection ends at its last child. Plain and quoted scalars end at their last character, excluding trailing comments. Flow collections end at the matching bracket. Block scalars end by indentation. If you touch `doc.go`, run the corpus test.

## Conventions

- Go, standard library plus `go.yaml.in/yaml/v3`. `spf13/cobra` is used for shell completion only (`complete.go`); argument parsing and dispatch stay in `cli.go`. Prefer an established library over hand-rolling shell or protocol code.
- New commands and flags go in `commands` (`cli.go`) and `flagDefs` (`help.go`); help and completion are generated from them.
- Keep the diff small and add a test in `internal/oq/cli_test.go` for any behaviour change. Add a fixture to `testdata/` for any new YAML shape.
- Commit messages are a single line.
- When behaviour or flags change, update `README.md`, `man/oq.1` and the help text in `internal/oq/help.go` together.
