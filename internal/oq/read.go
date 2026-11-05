package oq

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

func finishList(recs []Rec, o *opts, hint string) (*result, error) {
	data, err := render(recs, o, false)
	if err != nil {
		return nil, err
	}
	return &result{data, hint}, nil
}

func nodeRec(w *WS, n *Node) Rec {
	l1, l2 := n.lines()
	return Rec{
		{"address", w.Addr(n)}, {"kind", kindOf(n.Val)}, {"tokens", n.tokens()},
		{"line", l1}, {"endLine", l2}, {"note", n.note()},
	}
}

func cmdOutline(c *cli, o *opts) (*result, error) {
	if err := need(o, 1, 2, "oq outline <spec> [addr] [--depth N]"); err != nil {
		return nil, err
	}
	w, err := c.workspace(o.pos[0])
	if err != nil {
		return nil, err
	}
	addr := "/"
	if len(o.pos) == 2 {
		addr = o.pos[1]
	}
	n, err := w.Resolve(addr)
	if err != nil {
		return nil, err
	}
	depth := o.depth
	var recs []Rec
	var walk func(m *Node, d int)
	walk = func(m *Node, d int) {
		for _, k := range m.Kids {
			recs = append(recs, nodeRec(w, k))
			if d < depth {
				walk(k, d+1)
			}
		}
	}
	walk(n, 1)
	if len(recs) == 0 {
		recs = append(recs, nodeRec(w, n))
	}
	hint := ""
	switch {
	case depth > 1:
		hint = fmt.Sprintf("oq outline %s %s --depth %d, or outline a child address", shq(o.pos[0]), shq(addr), depth-1)
	case len(n.Kids) > 0:
		hint = fmt.Sprintf("outline one child, e.g. oq outline %s %s, or filter with grep/awk (oq ls ops | grep ...)", shq(o.pos[0]), shq(w.Addr(n.Kids[0])))
	}
	return finishList(recs, o, hint)
}

func cmdGet(c *cli, o *opts) (*result, error) {
	if err := need(o, 2, 2, "oq get <spec> <addr>"); err != nil {
		return nil, err
	}
	w, err := c.workspace(o.pos[0])
	if err != nil {
		return nil, err
	}
	n, err := w.Resolve(o.pos[1])
	if err != nil {
		return nil, err
	}
	text := n.DisplayText()
	hint := ""
	if len(n.Kids) > 0 {
		hint = fmt.Sprintf("oq outline %s %s  (child sizes), then oq get a child", shq(o.pos[0]), shq(w.Addr(n)))
	}
	meta := o.metadata || o.format == "columns" || o.format == "name"
	if o.header && !meta {
		return nil, fail(ExitUsage, "--header on get needs --metadata (raw text has no columns)")
	}
	if !meta {
		if o.tsv {
			return nil, fail(ExitUsage, "get prints the node's raw text; for a tsv row of metadata use --metadata")
		}
		if o.format == "json" {
			j, err := toJSON(n.Val, true, false, "  ", 0)
			if err != nil {
				return nil, fail(ExitUsage, "%s: %v", w.Addr(n), err)
			}
			text = j
		}
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return &result{[]byte(text), hint}, nil
	}
	l1, c1 := n.Doc.Pos(n.ValStart)
	l2, c2 := n.Doc.Pos(n.ValEnd)
	rec := Rec{
		{"address", w.Addr(n)}, {"kind", kindOf(n.Val)}, {"file", fileLabel(w, n.Doc)},
		{"line", l1}, {"col", c1}, {"endLine", l2}, {"endCol", c2},
		{"startByte", n.ValStart}, {"endByte", n.ValEnd},
		{"hash", hashOf(text)}, {"tokens", tokensOf(text)},
	}
	data, err := render([]Rec{rec}, o, true)
	if err != nil {
		return nil, err
	}
	return &result{data, hint}, nil
}

func fileLabel(w *WS, d *Doc) string {
	if d.Abs == "" {
		return "-"
	}
	return w.rel(d.Abs)
}

func cmdAt(c *cli, o *opts) (*result, error) {
	if err := need(o, 2, 2, "oq at <spec> <line>[:<col>]"); err != nil {
		return nil, err
	}
	w, err := c.workspace(o.pos[0])
	if err != nil {
		return nil, err
	}
	ls, cs, hasCol := strings.Cut(o.pos[1], ":")
	line, err := strconv.Atoi(ls)
	if err != nil || line < 1 {
		return nil, fail(ExitUsage, "bad line %q", ls)
	}
	col := 0
	if hasCol {
		if col, err = strconv.Atoi(cs); err != nil || col < 1 {
			return nil, fail(ExitUsage, "bad column %q", cs)
		}
	}
	d := w.Root
	if line > len(d.lines) || (line == len(d.lines) && d.lines[line-1] >= len(d.Src)) {
		return nil, fail(ExitNotFound, "line %d is past the end of the file (%d lines)", line, len(d.lines)-1)
	}
	var p int
	if hasCol {
		p = d.off(line, col)
	} else {
		p = d.lines[line-1]
		for p < len(d.Src) && (d.Src[p] == ' ' || d.Src[p] == '\t') {
			p++
		}
	}
	cur := d.Root
	for {
		var next *Node
		for _, k := range cur.Kids {
			end := k.ValEnd
			if end == k.EntryStart {
				end++
			}
			if p >= k.EntryStart && p < end {
				next = k
				break
			}
		}
		if next == nil {
			break
		}
		cur = next
	}
	if o.format != "" {
		data, err := render([]Rec{nodeRec(w, cur)}, o, true)
		if err != nil {
			return nil, err
		}
		return &result{data, ""}, nil
	}
	return &result{[]byte(w.Addr(cur) + "\n"), ""}, nil
}

func cmdLs(c *cli, o *opts) (*result, error) {
	if err := need(o, 2, 2, "oq ls <spec> ops|schemas|tags|files"); err != nil {
		return nil, err
	}
	w, err := c.workspace(o.pos[0])
	if err != nil {
		return nil, err
	}
	var recs []Rec
	switch o.pos[1] {
	case "ops":
		for _, op := range w.Ops() {
			recs = append(recs, Rec{
				{"address", w.Addr(op.Node)}, {"method", strings.ToUpper(op.Method)}, {"path", op.Path},
				{"operationId", op.ID}, {"tokens", op.Node.tokens()},
			})
		}
	case "schemas":
		for _, b := range w.schemaBases() {
			for _, k := range b.Kids {
				recs = append(recs, Rec{{"address", w.Addr(k)}, {"name", k.Seg}, {"tokens", k.tokens()}})
			}
		}
	case "tags":
		count := map[string]int{}
		for _, op := range w.Ops() {
			if t := op.Node.Child("tags"); t != nil {
				for _, k := range t.Kids {
					count[k.Val.Value]++
				}
			}
		}
		if t := w.Root.Root.Child("tags"); t != nil {
			for _, k := range t.Kids {
				name := ""
				if nm := k.Child("name"); nm != nil {
					name = nm.Val.Value
				}
				recs = append(recs, Rec{{"address", w.Addr(k)}, {"name", name}, {"tokens", k.tokens()}, {"ops", count[name]}})
			}
		}
	case "files":
		docs := w.Closure()
		for _, d := range docs[1:] {
			recs = append(recs, Rec{{"path", d.Disp}, {"tokens", tokensOfBytes(d.Src)}, {"status", "ok"}})
		}
		miss := w.MissingFiles()
		for _, f := range slices.Sorted(maps.Keys(miss)) {
			st := "missing"
			if !strings.HasPrefix(miss[f].Error(), "file not found") {
				st = "parse-error"
			}
			recs = append(recs, Rec{{"path", f}, {"tokens", "-"}, {"status", st}})
		}
	default:
		return nil, fail(ExitUsage, "unknown listing %q (want ops, schemas, tags or files)", o.pos[1])
	}
	hint := fmt.Sprintf("oq outline %s /paths  (or filter this listing with grep/awk)", shq(o.pos[0]))
	return finishList(recs, o, hint)
}

func cmdDeps(c *cli, o *opts) (*result, error) {
	if err := need(o, 2, 2, "oq deps <spec> <addr> [--depth N] [--unresolved]"); err != nil {
		return nil, err
	}
	w, err := c.workspace(o.pos[0])
	if err != nil {
		return nil, err
	}
	start, err := w.Resolve(o.pos[1])
	if err != nil {
		return nil, err
	}
	depth := o.depth
	type dep struct {
		t     RefTarget
		depth int
		from  string
	}
	var deps []dep
	visited := map[string]bool{w.Addr(start): true}
	frontier := []*Node{start}
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []*Node
		for _, f := range frontier {
			var sites []RefSite
			refSites(f, &sites)
			for _, s := range sites {
				t := w.ParseRef(f.Doc, s.Raw)
				key := w.TargetAddr(t)
				if visited[key] {
					continue
				}
				visited[key] = true
				deps = append(deps, dep{t, d, w.Addr(s.Node)})
				if t.Resolved() {
					next = append(next, t.Node)
				}
			}
		}
		frontier = next
	}
	var recs []Rec
	unresolved := 0
	for _, d := range deps {
		if !d.t.Resolved() {
			unresolved++
			if o.unresolved {
				recs = append(recs, Rec{{"address", w.TargetAddr(d.t)}, {"tokens", "-"}, {"depth", d.depth}, {"from", d.from}, {"reason", d.t.Reason}})
			}
			continue
		}
		if !o.unresolved {
			recs = append(recs, Rec{{"address", w.TargetAddr(d.t)}, {"tokens", d.t.Node.tokens()}, {"depth", d.depth}, {"from", d.from}})
		}
	}
	if unresolved > 0 && !o.unresolved {
		fmt.Fprintf(c.err, "oq: %d unresolved ref(s) skipped (list them with --unresolved)\n", unresolved)
	}
	hint := fmt.Sprintf("oq deps %s %s --depth %d", shq(o.pos[0]), shq(o.pos[1]), max(1, depth-1))
	return finishList(recs, o, hint)
}

func cmdRefs(c *cli, o *opts) (*result, error) {
	if err := need(o, 2, 2, "oq refs <spec> <addr>"); err != nil {
		return nil, err
	}
	w, err := c.workspace(o.pos[0])
	if err != nil {
		return nil, err
	}
	target, err := w.Resolve(o.pos[1])
	if err != nil {
		return nil, err
	}
	tsegs := target.Segs()
	var recs []Rec
	seen := map[*Node]bool{}
	for _, d := range w.Closure() {
		for _, s := range d.allRefs() {
			t := w.ParseRef(d, s.Raw)
			if t.Doc != target.Doc || !hasPrefix(t.Segs, tsegs) || seen[s.Node] {
				continue
			}
			seen[s.Node] = true
			recs = append(recs, Rec{
				{"address", w.Addr(s.Node)}, {"tokens", s.Node.tokens()},
				{"target", w.TargetAddr(t)}, {"in", w.enclosing(s.Node)},
			})
		}
	}
	return finishList(recs, o, "narrow the address, or filter with grep")
}

func hasPrefix(s, p []string) bool { return len(s) >= len(p) && slices.Equal(s[:len(p)], p) }

// enclosing names the operation or component a node lives in, or "-".
func (w *WS) enclosing(n *Node) string {
	segs := n.Segs()
	cut := 0
	switch {
	case len(segs) >= 3 && segs[0] == "paths" && isMethod(segs[2]):
		cut = 3
	case len(segs) >= 3 && segs[0] == "components":
		cut = 3
	case len(segs) >= 2 && (segs[0] == "definitions" || segs[0] == "parameters" || segs[0] == "responses"):
		cut = 2
	}
	if cut == 0 {
		return "-"
	}
	return w.AddrOf(n.Doc, segs[:cut])
}
