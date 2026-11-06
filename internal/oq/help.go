package oq

import (
	"fmt"
	"strings"
)

const helpText = `oq - address nodes in an OpenAPI document

usage: oq <command> <spec> [args]      (<spec> is a file or - for stdin;
                                       for read commands it may be omitted when stdin is piped)

read:
  oq outline <spec> [addr] [--depth N]     child addresses, kind, ~tokens
  oq ls      <spec> ops|schemas|tags|files one per line, with ~tokens
  oq get     <spec> <addr>                 exact source text; --metadata: range + hash
  oq at      <spec> <line>[:<col>]         innermost node at that position
  oq deps    <spec> <addr> [--depth N]     refs this node depends on
             [--unresolved]                only refs that do not resolve
  oq refs    <spec> <addr>                 nodes that reference this one

write (new content on stdin or --file; output to stdout unless -i):
  oq set     <spec> <addr> [--expect H] [-i]
  oq insert  <spec> <addr> [--after KEY] [--expect H] [-i]
  oq rm      <spec> <addr> [--expect H] [-i]

addresses:
  /components/schemas/item        JSON pointer (also #/...)
  schemas/item.yaml#/Item         pointer into an external file
  GET /items/{item_id}            operation by method and path
  path:/items/{item_id}           a path item: all its methods
  opid:listItems                  operation by operationId
  schema:item                     named schema

global: -o tsv|name|columns=a,b|json|yaml (default tsv, the cheapest), --max-tokens N (default 5000, 0 disables),
        --header (listings)
        oq <command> --help for the flags of one command
shell:  oq completion bash|zsh|fish|powershell   completion script (oq completion <shell> --help)
exit:   0 ok, 1 not found, 2 usage/parse error, 3 stale write, 4 over size cap
`

var cmdDesc = map[string]string{
	"outline": "List the children of a node (default: the document root) with kind, approximate tokens and line range.\nA node with no children lists itself.",
	"ls":      "List operations, schemas, tags or the external files the spec references, one per line.",
	"get":     "Print the exact source text of one node (block collections are dedented). -o json prints the node converted\nto JSON, -o yaml the same as the default. --metadata prints address, range, hash and tokens instead.",
	"at":      "Print the address of the innermost node at a line (its first non-blank character) or at LINE:COL.",
	"deps":    "List the nodes a node references, breadth first, with approximate tokens. Default depth 1.",
	"refs":    "List the nodes that reference a node (or anything inside it), and the operation or component they are in.",
	"set":     "Replace the value at an address with YAML or JSON from stdin (or --file). Writes the whole file to stdout unless -i.",
	"insert":  "Add the entries of the input mapping (or items of the input sequence) to a container. Appends unless --after.",
	"rm":      "Remove the entry at an address, including its key.",
}

// flagDef describes one flag. Help, parsing and shell completion all read
// flagDefs, keyed by the name used in cmdSpec.flags.
type flagDef struct {
	long, short string // without dashes
	arg         string // value placeholder, "" for boolean flags
	desc        string
}

var flagDefs = map[string]flagDef{
	"--depth":      {"depth", "", "N", "how many levels to descend (default 1)"},
	"--header":     {"header", "", "", "print column names as the first line"},
	"--metadata":   {"metadata", "", "", "print the node's metadata (address, kind, range, hash, tokens) instead of its text,\n                    in the -o format (tsv row by default). -o columns=hash implies it"},
	"--unresolved": {"unresolved", "", "", "list only refs that do not resolve (adds a reason column)"},
	"--expect":     {"expect", "", "H", "fail with exit 3 unless the node's hash (from get --metadata -o columns=hash) starts with H"},
	"-i":           {"in-place", "i", "", "edit the file atomically instead of writing to stdout"},
	"--after":      {"after", "", "KEY", "insert after this key (mapping) or index (sequence)"},
	"--file":       {"file", "f", "F", "read the new content from F instead of stdin"},
	"-o":           {"output", "o", "F", "tsv (default), name (first column only, e.g. addresses),\n                    columns=a,b (named columns), json or yaml. json/yaml repeat field\n                    names on every record (~70% more tokens on listings): prefer the default"},
	"--max-tokens": {"max-tokens", "", "N", "output cap in approximate tokens (default 5000, 0 disables)"},
	"-h":           {"help", "h", "", "this help"},
}

// cmdFlags lists the flags a command accepts, as flagDefs keys, in help order.
func cmdFlags(sp cmdSpec) []string {
	fs := append(strings.Fields(sp.flags), "-o")
	if sp.read {
		fs = append(fs, "--max-tokens")
	}
	return append(fs, "-h")
}

func (f flagDef) String() string {
	name := "--" + f.long
	if f.short != "" {
		name = "-" + f.short + ", " + name
	}
	if f.arg != "" {
		name += " " + f.arg
	}
	return fmt.Sprintf("%-18s%s", name, f.desc)
}

func cmdHelp(name string, sp cmdSpec) string {
	var b strings.Builder
	b.WriteString("usage: " + sp.usage + "\n\n" + cmdDesc[name] + "\n\nflags:\n")
	for _, f := range cmdFlags(sp) {
		b.WriteString("  " + flagDefs[f].String() + "\n")
	}
	if sp.read {
		b.WriteString("\nWith stdin piped, <spec> may be omitted.\n")
	}
	return b.String()
}
