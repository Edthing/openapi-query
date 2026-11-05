# oq

Address nodes in an OpenAPI document. `oq` is a small, agent-friendly CLI: every command either resolves an address or acts on the text at one. List what is there and how big it is, print it exactly, map a line number back to a node, or replace it.

Searching, linting, formatting, bundling and summarising are other tools' jobs. `oq` is built to sit in a pipe next to them.

```console
$ oq ls petstore.yaml ops
/paths/~1pets/get             GET   /pets            listPets   ~94
/paths/~1pets/post            POST  /pets            createPet  ~65
/paths/~1pets~1{pet_id}/get   GET   /pets/{pet_id}   getPet     ~92

$ oq get petstore.yaml opid:getPet
operationId: getPet
responses:
  "200":
  ...
```

## Why

Coding agents read OpenAPI specs badly. A person opens a spec layer by layer: paths, then one operation, then the one schema they care about. An agent reads the whole file, pages through line ranges it has to guess, or delegates to a worker that reads everything and returns a summary.

Real specs are big. On a 1.08 MB, 30,729-line public spec (about 270k tokens), `oq ls ops` lists all 92 operations in about 50 ms, and `oq get` on one operation returns about 940 tokens. No LLM is needed to fetch an exact answer.

`yq` and `jq` can fetch any node, but they have no notion of folding, never report sizes, and `yq -i` re-serialises the whole file. `oq` reports sizes first, prints source text exactly, and edits by splicing only the target node's byte range.

## Install

With Go 1.24 or newer:

```sh
go install github.com/Edwinexd/openapi-query/cmd/oq@latest
```

From a checkout:

```sh
make build            # ./oq
make install          # ~/.local/bin/oq and the man page, no root needed
                      # system-wide: sudo make install PREFIX=/usr/local
```

Release binaries (Linux, macOS, Windows; amd64 and arm64) are built with GoReleaser (`.goreleaser.yaml`) and attached to tagged GitHub releases. `oq` is a single static binary with one dependency (`go.yaml.in/yaml/v3`) and makes no network calls.

## Commands

```
read:
  oq outline <spec> [addr] [--depth N]     child addresses, kind, ~tokens
  oq ls      <spec> ops|schemas|tags|files one per line, with ~tokens
  oq get     <spec> <addr>                 exact source text; --metadata: range + hash
  oq at      <spec> <line>[:<col>]         innermost node at that position
  oq deps    <spec> <addr> [--depth N]     refs this node depends on, with ~tokens
             [--unresolved]                only refs that do not resolve
  oq refs    <spec> <addr>                 nodes that reference this one

write (new content on stdin or --file F; the whole file goes to stdout unless -i):
  oq set     <spec> <addr> [--expect H] [-i]
  oq insert  <spec> <addr> [--after KEY] [--expect H] [-i]
  oq rm      <spec> <addr> [--expect H] [-i]

global: -o tsv|name|columns=a,b|json|yaml (default tsv), --max-tokens N (default 5000, 0 disables),
        --header (listings), -h/--help
```

`oq <command> --help` (or `oq help <command>`) shows the usage and flags of one command.

`<spec>` is a file, or `-` for stdin. For read commands the spec may be left out when stdin is piped, so this works:

```sh
cat openapi.yaml | oq outline
curl -s https://example.com/openapi.json | oq get /info
```

If the first argument is an existing file it is the spec; otherwise it is the address and the spec comes from stdin. Write commands read the new content from stdin, so they always need an explicit spec.

### Addresses

One grammar for every command:

```
/components/schemas/item           JSON pointer (also #/...)
schemas/item.yaml#/Item            pointer into an external file (relative to the spec's directory)
GET /items/{item_id}               operation by method and path
path:/items/{item_id}              the whole path item, all methods
opid:listItems                     operation by operationId
schema:item                        named schema (components/schemas, or definitions in Swagger 2.0)
```

Everything `oq` prints as an address is a JSON pointer with no spaces, quotes or backslashes, so it is safe for `xargs`. Awkward characters are percent-encoded (`odd%20key~1with%20space`), and every address `oq` prints is accepted by `oq`. Addresses into other files print as `path/to/file.yaml#/pointer`.

### Output

Listings are one record per line, tab-separated, so `cut`, `sort`, `awk` and `xargs` work. There is no header line by default (it would be a fake record in a pipe); add `--header` to `outline`, `ls`, `deps` or `refs` to print the column names first. Columns:

| command   | columns |
|-----------|---------|
| `outline` | address, kind, ~tokens, line, endLine, note (`&anchor` or `*alias`) |
| `ls ops`  | address, method, path, operationId, ~tokens |
| `ls schemas` | address, name, ~tokens |
| `ls tags` | address, name, ~tokens, ops |
| `ls files` | path, ~tokens, status |
| `deps`    | address, ~tokens, depth, from (`--unresolved` adds a reason column) |
| `refs`    | address of the referencing node, ~tokens, target, in (enclosing operation or component) |

`get` prints the node's source text and nothing else, so it pipes straight into `yq`, `grep` or another tool. Block mappings and sequences are dedented so the output stands alone as YAML. `-o name` prints only the first column (the address, for most commands), and `-o columns=address,tokens` picks and orders columns by the names `--header` shows, so scripts do not depend on column positions:

```sh
oq deps spec.yaml opid:getPet -o name | xargs -I% oq get spec.yaml %
oq ls spec.yaml schemas -o columns=name,tokens --header
```

`get` prints the node itself. By default that is the raw source text; `-o yaml` is the same text, and `-o json` is the node converted to JSON (pretty-printed, key order kept; aliases are not expanded, so a node containing one is an error). Add `--metadata` to print the node's metadata instead of its text, in the format `-o` names: a tsv row by default (`--header` adds the column names), or a flat record with `-o json` or `-o yaml`. Metadata fields: `address`, `kind`, `file`, `line`, `col`, `endLine`, `endCol`, `startByte`, `endByte`, `hash`, `tokens`. `-o columns=hash` implies `--metadata`.

`-o json` and `-o yaml` (long form `--output`) are available on every command for programs that want structured data. They repeat every field name on every record (about 70% more tokens than the default on `ls ops`), so agents should prefer the default text. There is deliberately no `--json` flag. The hash needed for edits does not need JSON: `oq get spec addr -o columns=hash` prints just the hash.

Token counts are characters / 4 and are approximate.

### Size cap and exit codes

If output would exceed `--max-tokens` (default 5000), `oq` prints nothing to stdout, reports the size and a narrower command on stderr, and exits `4`. It never truncates mid-node and never silently returns something else.

| code | meaning |
|------|---------|
| 0 | ok |
| 1 | address not found (like grep's "no match") |
| 2 | usage or parse error |
| 3 | stale write (`--expect` hash mismatch) |
| 4 | output over the size cap |

Errors go to stderr. Success is silent: no banners, no diffs (use `diff`).

## Composition

```sh
# search is grep's job; oq maps hits to nodes
grep -n 'deprecated: true' openapi.yaml | cut -d: -f1 | xargs -n1 oq at openapi.yaml

# a linter reports a line; open the node it belongs to
oq get openapi.yaml "$(oq at openapi.yaml 812)"

# one operation plus everything it references, one level deep
oq deps openapi.yaml 'GET /items/{item_id}' --depth 1 -o name \
  | xargs -I% oq get openapi.yaml %

# every operation that uses a schema
oq refs openapi.yaml schema:Pet | cut -f4 | sort -u

# which refs are broken
oq deps openapi.yaml / --unresolved
```

## Editing

Edits behave like `sed`: the modified document goes to stdout by default, and `-i` edits in place atomically. A write splices the original text and re-indents the new block to its position. Bytes outside the target node's range are never touched, comments and formatting included.

```sh
# read with -o json to get the hash
h=$(oq get openapi.yaml schema:item -o columns=hash)

# replace the node, but only if nobody changed it since
oq set openapi.yaml schema:item --expect "$h" -i < item.yaml

# add schemas (the input is a mapping; keys must not already exist)
oq insert openapi.yaml /components/schemas --after item -i < more.yaml

# remove a node
oq rm openapi.yaml 'opid:legacyThing' -i
```

- `set` replaces the value at an address. The input is a YAML (or JSON) document and may be a scalar, a block collection or a flow collection.
- `insert` adds the entries of the input mapping (or items of the input sequence) to a container. With `--after KEY` they go after that key (or after that index, for a sequence), otherwise at the end.
- `rm` removes the entry, including its key.
- In a JSON file, new content is written as JSON and matches the file's compact or pretty-printed style.
- If the spec is on stdin (`-`), pass the new content with `--file F`. `-i` needs a real file.
- If the address points into an external file (`schemas/item.yaml#/Item`), that file is the one edited or printed.
- Anchors and aliases are reported and never edited through. `set` on an anchored mapping keeps the `&anchor` header.
- `--expect H` takes the hash from `get -o columns=hash` (6 or more hex characters). A mismatch exits `3`. `-i` writes under a directory lock, re-reads the file before the rename, and re-applies the edit (including the hash check) if the file changed in between, so concurrent agents on different nodes do not clobber each other.
- Before anything is written, `oq` checks that the result parses and that the target node holds exactly the value you supplied. If not, it refuses and exits `2`. Anything semantic is composed from other tools:

```sh
oq set openapi.yaml schema:item --expect "$h" < item.yaml > tmp.yaml \
  && [ -z "$(oq deps tmp.yaml / --unresolved)" ] \
  && redocly lint tmp.yaml \
  && mv tmp.yaml openapi.yaml
```

With `-i -o json` (or `yaml`), the result is `{"address","file","hash"}`, with the hash of the node (or its parent, for `rm`) after the edit, so the next edit can chain.

## For coding agents

Put this in `AGENTS.md`, `CLAUDE.md` or your agent's system prompt (also in [`docs/agent-snippet.md`](docs/agent-snippet.md)). For agents that load skills, a ready-made one is in [`examples/skills/oq/SKILL.md`](examples/skills/oq/SKILL.md); copy the `oq` folder into your skills directory (for example `~/.claude/skills/`).

```markdown
## OpenAPI specs

Never read an OpenAPI file whole or page through it. Use `oq` (addresses nodes, prints exact text):

- `oq ls SPEC ops` / `oq ls SPEC schemas`: find things; the last column is ~tokens.
- `oq outline SPEC [ADDR]`: children of a node with sizes. Unfold step by step.
- `oq get SPEC ADDR`: the exact text of one node. ADDR is `/json/pointer`, `GET /path`, `opid:name` or `schema:Name`.
- `oq deps SPEC ADDR --depth 1`: what a node references; `oq refs SPEC ADDR`: what references it.
- `oq at SPEC LINE`: which node a line belongs to (use with grep -n and linter output).
- Exit 4 means the answer was over the size cap: narrow the address or use `outline`. Do not retry with `--max-tokens 0`.
- To edit, `oq get SPEC ADDR -o columns=hash` first and keep the hash, then
  `oq set SPEC ADDR --expect HASH -i < new.yaml` (also `insert`, `rm`). Exit 3 means the node changed: re-read it.
  Never hand-edit a large spec file.
```

Raw `get` output has no hash, so agents that edit must also read the hash with `-o columns=hash`.

## Limits

- Input is YAML or JSON with a single document. OpenAPI 3.0 and 3.1 are the target. Swagger 2.0 works for pointers, operations and `schema:` (`definitions`).
- `$ref`s are followed across local files only. Remote (`http://`) refs are never fetched and show up as unresolved in `deps`. Non-pointer fragments (`#anchorName`) are also unresolved.
- A node's end is found without re-serialising: it stops before trailing blank lines and comments. A comment that sits between two siblings stays where it is on edits.
- A `|+` (keep) block scalar loses its trailing blank lines in raw `get` output. Writing such a scalar back is refused, and rewriting a larger node that ends in one drops those blank lines.
- `at` with a line only uses the first non-blank character of the line, so for `- name: x` it returns the sequence item. Pass `LINE:COL` for the key.

## Development

```sh
go test ./...                       # golden/exit-code tests on testdata/
make corpus DIR=/path/to/specs      # slice + round-trip checks over a real corpus
```

The corpus test checks that every node's slice parses back to the same value as the parser's node, and that writing a node back unchanged leaves the file byte-identical. A good corpus is the [openapi-completion-refined](https://huggingface.co/datasets/BohdanPetryshyn/openapi-completion-refined) dataset of 990 real specs (see `AGENTS.md`).

## License

MIT
