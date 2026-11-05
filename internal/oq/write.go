package oq

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// newText is replacement content read from stdin or --file.
type newText struct {
	lines []string
	doc   *Doc
}

func (t *newText) y() *yaml.Node { return t.doc.Root.Val }

func (t *newText) blockColl() bool { return isColl(t.y()) && !isFlowColl(t.y()) }

func prepText(raw []byte) (*newText, error) {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	i := 0
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") || t == "---" {
			i++
			continue
		}
		break
	}
	lines = lines[i:]
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil, fail(ExitUsage, "new content is empty")
	}
	ind := len(lines[0]) - len(strings.TrimLeft(lines[0], " "))
	if ind > 0 {
		for j, l := range lines {
			if strings.TrimSpace(l) == "" {
				lines[j] = ""
			} else if strings.HasPrefix(l, strings.Repeat(" ", ind)) {
				lines[j] = l[ind:]
			}
		}
	}
	d, err := ParseDoc("", "", []byte(strings.Join(lines, "\n")+"\n"))
	if err != nil {
		return nil, fail(ExitUsage, "new content does not parse: %v", err)
	}
	return &newText{lines: lines, doc: d}, nil
}

func sp(n int) string {
	if n < 0 {
		n = 0
	}
	return strings.Repeat(" ", n)
}

// joinPref joins lines; the first gets `first`, other non-blank lines `rest`.
func joinPref(lines []string, first, rest, eol string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		switch {
		case l == "":
		case i == 0:
			out[i] = first + l
		default:
			out[i] = rest + l
		}
	}
	return strings.Join(out, eol)
}

// restP is the prefix for continuation lines of a value that is not a block
// collection: the entry's column, plus u when the text has no relative indent.
func restP(lines []string, base, u int) string {
	m := -1
	for _, l := range lines[min(1, len(lines)):] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if i := len(l) - len(strings.TrimLeft(l, " ")); m < 0 || i < m {
			m = i
		}
	}
	if m == 0 {
		return sp(base + u)
	}
	return sp(base)
}

func (d *Doc) lineIndent(p int) string {
	s := d.lineStart(p)
	e := s
	for e < len(d.Src) && (d.Src[e] == ' ' || d.Src[e] == '\t') {
		e++
	}
	return string(d.Src[s:e])
}

// indentUnit guesses the document's YAML indentation step.
func (d *Doc) indentUnit() int {
	if d.unit > 0 {
		return d.unit
	}
	d.unit = 2
	var walk func(n *Node) bool
	walk = func(n *Node) bool {
		if n.Key != nil && isColl(n.Val) && !isFlowColl(n.Val) {
			l1, _ := d.Pos(n.EntryStart)
			l2, _ := d.Pos(n.ValStart)
			if l2 > l1 {
				if u := d.col(n.ValStart) - d.col(n.EntryStart); u > 0 {
					d.unit = u
					return true
				}
			}
		}
		for _, k := range n.Kids {
			if walk(k) {
				return true
			}
		}
		return false
	}
	walk(d.Root)
	return d.unit
}

func (d *Doc) jsonUnit() string {
	for i := 1; i < len(d.lines); i++ {
		if d.lines[i] >= len(d.Src) {
			break
		}
		if ws := d.lineIndent(d.lines[i]); ws != "" {
			return ws
		}
	}
	return "  "
}

func (d *Doc) pretty() bool { return bytes.Contains(bytes.TrimSpace(d.Src), []byte("\n")) }

func splice(src []byte, a, b int, repl string) []byte {
	out := make([]byte, 0, len(src)-(b-a)+len(repl))
	out = append(out, src[:a]...)
	out = append(out, repl...)
	return append(out, src[b:]...)
}

// ---- set ----

var propsLine = regexp.MustCompile(`^\s*([&!][^\s]*\s*)+$`)

// hasHeader reports a block collection whose anchor/tag sits on the key line.
func (n *Node) hasHeader() bool {
	if !isColl(n.Val) || isFlowColl(n.Val) || len(n.Kids) == 0 || n.Key == nil {
		return false
	}
	l1, _ := n.Doc.Pos(n.ValStart)
	l2, _ := n.Doc.Pos(n.Kids[0].EntryStart)
	return l2 > l1
}

// region is a byte range of the document and the text that replaces it.
type region struct {
	a, b int
	repl string
}

func (n *Node) setEdit(t *newText) ([]byte, error) {
	d := n.Doc
	var r region
	switch {
	case n.hasHeader() && t.blockColl() && (n.Val.Anchor != "" || n.Val.Style&yaml.TaggedStyle != 0):
		var err error
		if r, err = n.setBody(t); err != nil {
			return nil, err
		}
	case n.flowParent() || d.JSONish:
		s, err := t.flowText(n)
		if err != nil {
			return nil, err
		}
		r = region{n.ValStart, n.ValEnd, s}
	case n.Parent == nil:
		r = region{n.ValStart, n.ValEnd, joinPref(t.lines, "", sp(d.col(n.ValStart)), d.EOL)}
	case n.Key != nil:
		r = n.setMapValue(t)
	default:
		r = n.setSeqItem(t)
	}
	out := splice(d.Src, r.a, r.b, r.repl)
	if err := verifyAt(out, n.Segs(), func(m *Node) error {
		if !equalNode(m.Val, t.y()) {
			return fmt.Errorf("written value does not match the input")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// setBody replaces only the children of a block collection whose anchor or
// tag sits on the key line, keeping that header.
func (n *Node) setBody(t *newText) (region, error) {
	d := n.Doc
	lines := t.lines
	hdr := strings.TrimSpace(string(d.Src[n.ValStart:d.lineEnd(n.ValStart)]))
	if i := strings.Index(hdr, " #"); i >= 0 {
		hdr = strings.TrimSpace(hdr[:i])
	}
	if len(lines) > 0 && propsLine.MatchString(lines[0]) {
		if strings.Join(strings.Fields(lines[0]), " ") != strings.Join(strings.Fields(hdr), " ") {
			return region{}, fail(ExitUsage, "input changes the anchor/tag %q of this node; refusing", hdr)
		}
		lines = lines[1:]
	}
	c := d.col(n.Kids[0].EntryStart)
	return region{d.lineStart(n.Kids[0].EntryStart), n.ValEnd, joinPref(lines, sp(c), sp(c), d.EOL)}, nil
}

// setMapValue places new content as the value of a block mapping entry.
func (n *Node) setMapValue(t *newText) region {
	d, eol, lines := n.Doc, n.Doc.EOL, t.lines
	ki, u := d.col(n.EntryStart), d.indentUnit()
	empty := n.ValStart == n.ValEnd
	sameLine := !bytes.Contains(d.Src[n.AfterColon:n.ValStart], []byte("\n"))
	switch {
	case t.blockColl() && sameLine:
		// move a trailing "# comment" up to the key line
		b, cm := n.ValEnd, ""
		if e := d.lineEnd(b); b < e {
			if tail := strings.TrimSpace(string(d.Src[b:e])); strings.HasPrefix(tail, "#") {
				cm, b = " "+tail, e
				if d.Src[e-1] == '\r' {
					b = e - 1
				}
			}
		}
		return region{n.AfterColon, b, cm + eol + joinPref(lines, sp(ki+u), sp(ki+u), eol)}
	case t.blockColl():
		c := d.col(n.ValStart)
		return region{d.lineStart(n.ValStart), n.ValEnd, joinPref(lines, sp(c), sp(c), eol)}
	case sameLine && !empty:
		return region{n.ValStart, n.ValEnd, joinPref(lines, "", restP(lines, ki, u), eol)}
	case sameLine:
		return region{n.ValStart, n.ValEnd, " " + joinPref(lines, "", restP(lines, ki, u), eol)}
	}
	return region{n.AfterColon, n.ValEnd, " " + joinPref(lines, "", restP(lines, ki, u), eol)}
}

// setSeqItem places new content as a block sequence item.
func (n *Node) setSeqItem(t *newText) region {
	d, eol, lines := n.Doc, n.Doc.EOL, t.lines
	switch {
	case n.ValStart == n.ValEnd:
		return region{n.ValStart, n.ValEnd, " " + joinPref(lines, "", restP(lines, d.col(n.EntryStart), 2), eol)}
	case t.blockColl():
		return region{n.ValStart, n.ValEnd, joinPref(lines, "", sp(d.col(n.ValStart)), eol)}
	}
	return region{n.ValStart, n.ValEnd, joinPref(lines, "", restP(lines, d.col(n.EntryStart), 2), eol)}
}

func verifyAt(out []byte, segs []string, check func(*Node) error) error {
	d, err := ParseDoc("", "", out)
	if err != nil {
		return fail(ExitUsage, "refusing to write: result does not parse: %v", err)
	}
	m := d.Root.Descend(segs)
	if m == nil {
		return fail(ExitUsage, "refusing to write: target missing after edit")
	}
	if err := check(m); err != nil {
		return fail(ExitUsage, "refusing to write: %v", err)
	}
	return nil
}

func equalNode(a, b *yaml.Node) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case yaml.ScalarNode:
		return a.ShortTag() == b.ShortTag() && a.Value == b.Value
	case yaml.AliasNode:
		return a.Value == b.Value
	}
	if len(a.Content) != len(b.Content) {
		return false
	}
	for i := range a.Content {
		if !equalNode(a.Content[i], b.Content[i]) {
			return false
		}
	}
	return true
}

// ---- insert ----

func (n *Node) insertEdit(t *newText, after string, hasAfter bool) ([]byte, error) {
	d := n.Doc
	eol := d.EOL
	y, ty := n.Val, t.y()
	nullEmpty := y.Kind == yaml.ScalarNode && y.Tag == "!!null" && y.Value == ""
	if !isColl(ty) {
		return nil, fail(ExitUsage, "insert expects a mapping or sequence as input")
	}
	if nullEmpty || (isColl(y) && len(n.Kids) == 0) {
		return n.setEdit(t)
	}
	if !isColl(y) || y.Kind != ty.Kind {
		return nil, fail(ExitUsage, "cannot insert a %s into a %s at %s", kindOf(ty), kindOf(y), ptrString(n.Segs()))
	}
	entries := t.doc.Root.Kids
	if y.Kind == yaml.MappingNode {
		for _, e := range entries {
			if n.Child(e.Seg) != nil {
				return nil, fail(ExitUsage, "key %q already exists at %s (use set)", e.Seg, ptrString(n.Segs()))
			}
		}
	}
	ref := n.Kids[len(n.Kids)-1]
	if hasAfter {
		ref = nil
		if y.Kind == yaml.MappingNode {
			ref = n.Child(after)
		} else if i, err := strconv.Atoi(after); err == nil && i >= 0 && i < len(n.Kids) {
			ref = n.Kids[i]
		}
		if ref == nil {
			return nil, fail(ExitNotFound, "--after %q: no such entry in %s", after, ptrString(n.Segs()))
		}
	}
	var a int
	var text string
	if isFlowColl(y) {
		pretty := bytes.Contains(d.Src[n.ValStart:n.ValEnd], []byte("\n"))
		tight := d.JSONish && !pretty
		li := d.lineIndent(ref.EntryStart)
		sep := ", "
		switch {
		case len(n.Kids) >= 2 && !bytes.Contains(d.Src[n.Kids[0].ValEnd:n.Kids[1].EntryStart], []byte("#")):
			sep = string(d.Src[n.Kids[0].ValEnd:n.Kids[1].EntryStart])
		case pretty:
			sep = "," + eol + li
		case tight:
			sep = ","
		}
		for _, e := range entries {
			s, err := toJSON(e.Val, pretty && d.JSONish, tight, d.jsonUnit(), 0)
			if err != nil {
				return nil, err
			}
			if y.Kind == yaml.MappingNode {
				sc := ": "
				if tight {
					sc = ":"
				}
				s = jsonStr(e.Seg) + sc + s
			}
			if pretty && d.JSONish {
				s = strings.ReplaceAll(s, "\n", eol+li)
			}
			text += sep + s
		}
		a = ref.ValEnd
	} else {
		lines := t.lines
		if isFlowColl(ty) {
			lines = nil
			for _, e := range entries {
				s, err := toJSON(e.Val, false, false, "  ", 0)
				if err != nil {
					return nil, err
				}
				if y.Kind == yaml.MappingNode {
					lines = append(lines, yamlKey(e.Seg)+": "+s)
				} else {
					lines = append(lines, "- "+s)
				}
			}
		}
		c := d.col(n.Kids[0].EntryStart)
		a = d.lineEnd(ref.ValEnd)
		if a > 0 && d.Src[a-1] == '\r' {
			a--
		}
		text = eol + joinPref(lines, sp(c), sp(c), eol)
	}
	out := splice(d.Src, a, a, text)
	want := len(n.Kids) + len(entries)
	if err := verifyAt(out, n.Segs(), func(m *Node) error {
		if len(m.Kids) != want {
			return fmt.Errorf("expected %d entries after insert, found %d", want, len(m.Kids))
		}
		for _, e := range entries {
			if y.Kind == yaml.MappingNode {
				k := m.Child(e.Seg)
				if k == nil || !equalNode(k.Val, e.Val) {
					return fmt.Errorf("inserted key %q does not match the input", e.Seg)
				}
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

var plainKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.\-]*$`)

func yamlKey(k string) string {
	if plainKey.MatchString(k) && k != "true" && k != "false" && k != "null" {
		return k
	}
	return jsonStr(k)
}

// ---- rm ----

func (n *Node) rmEdit() ([]byte, error) {
	d := n.Doc
	if n.Parent == nil {
		return nil, fail(ExitUsage, "cannot remove the document root")
	}
	var a, b int
	p := n.Parent
	idx := 0
	for i, k := range p.Kids {
		if k == n {
			idx = i
		}
	}
	if isFlowColl(p.Val) {
		switch {
		case idx+1 < len(p.Kids):
			a, b = n.EntryStart, p.Kids[idx+1].EntryStart
		case idx > 0:
			a, b = p.Kids[idx-1].ValEnd, n.ValEnd
		default:
			a, b = n.EntryStart, n.ValEnd
		}
	} else {
		ls := d.lineStart(n.EntryStart)
		own := len(bytes.TrimSpace(d.Src[ls:n.EntryStart])) == 0
		le := d.lineEnd(n.ValEnd)
		switch {
		case own:
			a, b = ls, le+1
			if b > len(d.Src) {
				b = len(d.Src)
			}
		case idx+1 < len(p.Kids):
			a, b = n.EntryStart, p.Kids[idx+1].EntryStart
		default:
			a, b = n.EntryStart, le
		}
	}
	out := splice(d.Src, a, b, "")
	want := len(p.Kids) - 1
	if err := verifyAt(out, p.Segs(), func(m *Node) error {
		if isColl(m.Val) && len(m.Kids) != want && want > 0 {
			return fmt.Errorf("expected %d entries after removal, found %d", want, len(m.Kids))
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}
