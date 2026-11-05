---
name: oq
description: Navigate and edit large OpenAPI specs (YAML or JSON) with the oq CLI instead of reading the file. Use when a task involves an OpenAPI/Swagger file: finding an operation or schema, tracing $refs, mapping a linter line to a node, or changing one node.
---

# Working with OpenAPI specs using oq

Never read an OpenAPI file whole or page through line ranges. `oq` addresses nodes and prints exact text with sizes, so fetch only what the question needs. Check `oq help` if a flag is unclear.

## Find

```sh
oq ls SPEC ops              # address, method, path, operationId, ~tokens
oq ls SPEC schemas          # address, name, ~tokens
oq ls SPEC ops | grep -i invoice
```

## Read, narrowing step by step

```sh
oq outline SPEC /paths                 # children with kind and ~tokens
oq get SPEC 'GET /items/{item_id}'     # exact source text of one node
oq outline SPEC path:/items/{item_id}  # all methods of one path, with sizes
oq get SPEC opid:listItems
oq get SPEC schema:Item
```

Addresses: `/json/pointer`, `file.yaml#/pointer`, `METHOD /path`, `path:/path`, `opid:NAME`, `schema:NAME`. Every address oq prints can be passed back to oq.

## Follow references

```sh
oq deps SPEC opid:getItem --depth 1 -o name | xargs -I% oq get SPEC %
oq refs SPEC schema:Item              # who uses this schema
oq deps SPEC / --unresolved           # broken refs
```

## Map a line to a node

```sh
grep -n 'deprecated: true' SPEC | cut -d: -f1 | xargs -n1 oq at SPEC
oq get SPEC "$(oq at SPEC 812)"       # open the node a linter line belongs to
```

## Size cap

Exit code 4 means the answer was over the cap (default ~5000 tokens). Nothing was printed. Follow the suggested narrower command, or use `outline`. Do not retry with `--max-tokens 0`.

## Edit

1. Read the hash and keep it: `oq get SPEC ADDR -o columns=hash`
2. Write the new node as YAML in a file, then:

```sh
oq set SPEC ADDR --expect HASH -i < new.yaml        # replace a node
oq insert SPEC /components/schemas --after Item -i < more.yaml
oq rm SPEC ADDR --expect HASH -i
```

- Exit 3: the node changed since you read it. Re-read, then redo the edit.
- Exit 2: the result would not have parsed or the input was invalid; nothing was written.
- Without `-i` the whole modified file goes to stdout, so you can validate before it lands:

```sh
oq set SPEC ADDR --expect HASH < new.yaml > tmp.yaml \
  && [ -z "$(oq deps tmp.yaml / --unresolved)" ] && mv tmp.yaml SPEC
```

Never hand-edit a large spec with a text editor tool when oq can do it.
