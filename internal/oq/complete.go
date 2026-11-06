package oq

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// Shell completion. Cobra supplies the shell scripts (oq completion bash|zsh|
// fish|powershell) and the hidden __complete protocol they call. The command
// tree below exists only for that: it is generated from commands and flagDefs,
// so completion cannot drift from the parser and help. Execution stays in run.

func isCompletionCmd(name string) bool {
	return name == "completion" || name == cobra.ShellCompRequestCmd || name == cobra.ShellCompNoDescRequestCmd
}

func (c *cli) runCompletion(args []string) (int, error) {
	root := &cobra.Command{
		Use:           "oq",
		Short:         "address nodes in an OpenAPI document",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.Flags().Bool("version", false, "print the version")
	root.AddCommand(&cobra.Command{Use: "version", Short: "print the version", Run: func(*cobra.Command, []string) {}})
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range commands {
			if !yield(k) {
				return
			}
		}
	}) {
		root.AddCommand(completionCmd(name, commands[name]))
	}
	root.SetArgs(args)
	root.SetIn(c.in)
	root.SetOut(c.out)
	root.SetErr(c.err)
	if err := root.Execute(); err != nil {
		return ExitUsage, err
	}
	return 0, nil
}

func completionCmd(name string, sp cmdSpec) *cobra.Command {
	cmd := &cobra.Command{
		Use:   name,
		Short: firstSentence(cmdDesc[name]),
		Run:   func(*cobra.Command, []string) {},
		ValidArgsFunction: func(_ *cobra.Command, args []string, cur string) ([]string, cobra.ShellCompDirective) {
			return completeArg(name, args, cur)
		},
	}
	for _, k := range cmdFlags(sp) {
		f := flagDefs[k]
		if f.long == "help" {
			continue // cobra adds its own
		}
		if f.arg == "" {
			cmd.Flags().BoolP(f.long, f.short, false, firstSentence(f.desc))
		} else {
			cmd.Flags().StringP(f.long, f.short, "", firstSentence(f.desc))
		}
		long := f.long
		cmd.RegisterFlagCompletionFunc(long, func(_ *cobra.Command, args []string, cur string) ([]string, cobra.ShellCompDirective) {
			return completeFlag(name, long, args, cur)
		})
	}
	return cmd
}

// firstSentence shortens help text to a one-line completion description.
func firstSentence(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	s, _, _ = strings.Cut(s, ". ")
	return strings.TrimRight(s, ".,")
}

const noFiles = cobra.ShellCompDirectiveNoFileComp

var specExts = []string{"yaml", "yml", "json"}

func completeArg(name string, args []string, cur string) ([]string, cobra.ShellCompDirective) {
	switch {
	case len(args) == 0:
		return specExts, cobra.ShellCompDirectiveFilterFileExt
	case len(args) > 1:
		return nil, noFiles
	case name == "ls":
		return []string{"ops\toperations", "schemas\tnamed schemas", "tags\ttop-level tags", "files\texternal files"}, noFiles
	case name == "at":
		return nil, noFiles
	}
	w := completionWS(args[0])
	if w == nil {
		return nil, noFiles
	}
	return w.completeAddr(cur)
}

func completeFlag(name, long string, args []string, cur string) ([]string, cobra.ShellCompDirective) {
	switch long {
	case "file":
		return nil, cobra.ShellCompDirectiveDefault
	case "output":
		if col, ok := strings.CutPrefix(cur, "columns="); ok {
			return completeColumns(name, args, col)
		}
		return []string{"tsv\tdefault, cheapest", "name\tfirst column only", "columns=\tnamed columns, e.g. columns=address,tokens", "json", "yaml"}, cobra.ShellCompDirectiveNoSpace | noFiles
	case "expect", "after":
		if len(args) < 2 {
			return nil, noFiles
		}
		w := completionWS(args[0])
		if w == nil {
			return nil, noFiles
		}
		n, err := w.Resolve(args[1])
		if err != nil {
			return nil, noFiles
		}
		if long == "expect" {
			return []string{hashOf(n.DisplayText()) + "\tcurrent hash of " + w.Addr(n)}, noFiles
		}
		var out []string
		for i, k := range n.Kids {
			if k.Key != nil {
				out = append(out, k.Seg+"\t"+kindOf(k.Val))
			} else {
				out = append(out, strconv.Itoa(i)+"\t"+kindOf(k.Val))
			}
		}
		return out, noFiles
	}
	return nil, noFiles
}

func completionWS(spec string) *WS {
	if spec == "-" {
		return nil
	}
	w, err := NewWS(spec, nil)
	if err != nil {
		return nil
	}
	return w
}

// completeColumns runs the command itself with -o json and offers the field
// names of its first record, so the list always matches the real output.
func completeColumns(name string, args []string, cur string) ([]string, cobra.ShellCompDirective) {
	sp, ok := commands[name]
	if !ok || !sp.read || len(args) < sp.min {
		return nil, noFiles
	}
	o := &opts{pos: args, format: "json", metadata: true, depth: 1}
	res, err := sp.fn(&cli{in: bytes.NewReader(nil), out: io.Discard, err: io.Discard}, o)
	if err != nil || res == nil {
		return nil, noFiles
	}
	keys := firstRecordKeys(res.data)
	done, _ := splitLast(cur, ",")
	var out []string
	for _, k := range keys {
		if !slices.Contains(strings.Split(done, ","), k) {
			out = append(out, "columns="+joinNonEmpty(done, k))
		}
	}
	return out, cobra.ShellCompDirectiveNoSpace | noFiles
}

func joinNonEmpty(a, b string) string {
	if a == "" {
		return b
	}
	return a + "," + b
}

// splitLast splits s at the last sep: ("a,b,c", ",") is ("a,b", "c").
func splitLast(s, sep string) (string, string) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):]
	}
	return "", s
}

// firstRecordKeys returns the keys of the first JSON object in data, in order.
func firstRecordKeys(data []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		t, err := dec.Token()
		if err != nil {
			return nil
		}
		if t == json.Delim('{') {
			break
		}
	}
	var keys []string
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return keys
		}
		keys = append(keys, t.(string))
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return keys
		}
	}
	return keys
}

// completeAddr offers addresses that extend cur, in every address form.
func (w *WS) completeAddr(cur string) ([]string, cobra.ShellCompDirective) {
	var out []string
	more := false // some candidate is a prefix the user will keep typing
	add := func(cand, desc string, container bool) {
		if strings.HasPrefix(cand, cur) {
			out = append(out, cand+"\t"+desc)
			more = more || container
		}
	}
	switch {
	case strings.HasPrefix(cur, "opid:"):
		for _, op := range w.Ops() {
			if op.ID != "" {
				add("opid:"+op.ID, strings.ToUpper(op.Method)+" "+op.Path, false)
			}
		}
	case strings.HasPrefix(cur, "path:"):
		if paths := w.Root.Root.Child("paths"); paths != nil {
			for _, k := range paths.Kids {
				add("path:"+k.Seg, k.tokens().String(), false)
			}
		}
	case strings.HasPrefix(cur, "schema:"):
		for _, b := range w.schemaBases() {
			for _, k := range b.Kids {
				add("schema:"+k.Seg, k.tokens().String(), false)
			}
		}
	case strings.HasPrefix(cur, "/") || strings.HasPrefix(cur, "#"):
		more = w.completePtr(w.Root, "", cur, &out)
	case strings.Contains(cur, "#"):
		file, _, _ := strings.Cut(cur, "#")
		if d := w.closureDoc(file); d != nil {
			more = w.completePtr(d, file+"#", cur, &out)
		}
	case cur != "" && cur[0] >= 'A' && cur[0] <= 'Z' && isMethodPrefix(cur):
		for _, op := range w.Ops() {
			add(strings.ToUpper(op.Method)+" "+op.Path, op.ID, false)
		}
	default:
		if cur == "" {
			more = w.completePtr(w.Root, "", "/", &out)
		}
		for _, p := range []string{"opid:", "path:", "schema:"} {
			add(p, "", true)
		}
		for _, d := range w.Closure()[1:] {
			add(d.Disp+"#", "external file", true)
		}
	}
	dir := noFiles
	if more {
		dir |= cobra.ShellCompDirectiveNoSpace
	}
	return out, dir
}

// isMethodPrefix reports whether s starts an operation address: "GE",
// "GET" or "GET /pe".
func isMethodPrefix(s string) bool {
	word, _, spaced := strings.Cut(strings.ToLower(s), " ")
	for _, m := range methods {
		if m == word || !spaced && strings.HasPrefix(m, word) {
			return true
		}
	}
	return false
}

func (w *WS) closureDoc(disp string) *Doc {
	for _, d := range w.Closure() {
		if d.Disp == disp {
			return d
		}
	}
	return nil
}

// completePtr appends pointers into d that extend cur (written with prefix,
// "" or "file#"). It completes one segment at a time like a path: the
// children of the parent of the last segment, and the children of cur itself
// when it already names a container. It reports whether any candidate has
// children, so the shell does not add a space after it.
func (w *WS) completePtr(d *Doc, prefix, cur string, out *[]string) bool {
	ptr := strings.TrimPrefix(cur, prefix)
	hash := strings.HasPrefix(ptr, "#")
	lead := prefix
	if hash {
		lead += "#"
		ptr = ptr[1:]
	}
	if ptr == "" {
		ptr = "/"
	}
	more := false
	list := func(parentPtr string) {
		n, err := w.resolvePtr(d, parsePointer(parentPtr), parentPtr)
		if err != nil {
			return
		}
		for _, k := range n.Kids {
			cand := lead + parentPtr + "/" + escSeg(k.Seg)
			if !strings.HasPrefix(cand, cur) || cand == cur && len(k.Kids) > 0 {
				continue
			}
			*out = append(*out, cand+"\t"+kindOf(k.Val)+" "+k.tokens().String())
			more = more || len(k.Kids) > 0
		}
	}
	parent, _ := splitLast(ptr, "/")
	list(parent)
	if ptr != "/" {
		if n, err := w.resolvePtr(d, parsePointer(ptr), ptr); err == nil && len(n.Kids) > 0 {
			list(ptr)
		}
	}
	return more
}
