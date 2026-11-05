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
	ind := leadSpaces(lines[0])
	if ind > 0 {
		for j, l := range lines {
			if strings.TrimSpace(l) == "" {
				lines[j] = ""
			} else if strings.HasPrefix(l, sp(ind)) {
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

func leadSpaces(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

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
		if i := leadSpaces(l); m < 0 || i < m {
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

// trailingComment returns a "# comment" that follows position p on its line,
// as text to move elsewhere, and the offset to cut up to (p if there is none).
func (d *Doc) trailingComment(p int) (string, int) {
	e := d.lineEnd(p)
	if p >= e {
		return "", p
	}
	tail := strings.TrimSpace(string(d.Src[p:e]))
	if !strings.HasPrefix(tail, "#") {
		return "", p
	}
	if d.Src[e-1] == '\r' {
		e--
	}
	return " " + tail, e
}

// setMapValue places new content as the value of a block mapping entry.
func (n *Node) setMapValue(t *newText) region {
	d, eol, lines := n.Doc, n.Doc.EOL, t.lines
	ki, u := d.col(n.EntryStart), d.indentUnit()
	sameLine := !bytes.Contains(d.Src[n.AfterColon:n.ValStart], []byte("\n"))
	switch {
	case t.blockColl() && sameLine:
		cm, end := d.trailingComment(n.ValEnd) // keep the comment on the key line
		return region{n.AfterColon, end, cm + eol + joinPref(lines, sp(ki+u), sp(ki+u), eol)}
	case t.blockColl():
		c := d.col(n.ValStart)
		return region{d.lineStart(n.ValStart), n.ValEnd, joinPref(lines, sp(c), sp(c), eol)}
	}
	body := joinPref(lines, "", restP(lines, ki, u), eol)
	switch {
	case sameLine && n.ValStart != n.ValEnd: // replace the inline value
		return region{n.ValStart, n.ValEnd, body}
	case sameLine: // "key:" with no value yet
		return region{n.ValStart, n.ValEnd, " " + body}
	}
	return region{n.AfterColon, n.ValEnd, " " + body}
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
	d, y, ty := n.Doc, n.Val, t.y()
	if !isColl(ty) {
		return nil, fail(ExitUsage, "insert expects a mapping or sequence as input")
	}
	if nullEmpty := y.Kind == yaml.ScalarNode && y.Tag == "!!null" && y.Value == ""; nullEmpty || (isColl(y) && len(n.Kids) == 0) {
		return n.setEdit(t)
	}
	if !isColl(y) || y.Kind != ty.Kind {
		return nil, fail(ExitUsage, "cannot insert a %s into a %s at %s", kindOf(ty), kindOf(y), ptrString(n.Segs()))
	}
	entries := t.doc.Root.Kids
	isMap := y.Kind == yaml.MappingNode
	if isMap {
		for _, e := range entries {
			if n.Child(e.Seg) != nil {
				return nil, fail(ExitUsage, "key %q already exists at %s (use set)", e.Seg, ptrString(n.Segs()))
			}
		}
	}
	ref := n.Kids[len(n.Kids)-1]
	if hasAfter {
		ref = nil
		if isMap {
			ref = n.Child(after)
		} else if i, err := strconv.Atoi(after); err == nil && i >= 0 && i < len(n.Kids) {
			ref = n.Kids[i]
		}
		if ref == nil {
			return nil, fail(ExitNotFound, "--after %q: no such entry in %s", after, ptrString(n.Segs()))
		}
	}
	var at int
	var text string
	var err error
	if isFlowColl(y) {
		at, text, err = n.insertFlow(entries, ref)
	} else {
		at, text, err = n.insertBlock(t, entries, ref)
	}
	if err != nil {
		return nil, err
	}
	out := splice(d.Src, at, at, text)
	want := len(n.Kids) + len(entries)
	if err := verifyAt(out, n.Segs(), func(m *Node) error {
		if len(m.Kids) != want {
			return fmt.Errorf("expected %d entries after insert, found %d", want, len(m.Kids))
		}
		if !isMap {
			return nil
		}
		for _, e := range entries {
			if k := m.Child(e.Seg); k == nil || !equalNode(k.Val, e.Val) {
				return fmt.Errorf("inserted key %q does not match the input", e.Seg)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// insertFlow renders entries for a JSON/flow container and returns where to
// splice them: after ref, separated the way the container already separates.
func (n *Node) insertFlow(entries []*Node, ref *Node) (int, string, error) {
	d, eol := n.Doc, n.Doc.EOL
	multiline := bytes.Contains(d.Src[n.ValStart:n.ValEnd], []byte("\n"))
	pretty := multiline && d.JSONish
	tight := d.JSONish && !multiline
	li := d.lineIndent(ref.EntryStart)
	sep := ", "
	switch {
	case len(n.Kids) >= 2 && !bytes.Contains(d.Src[n.Kids[0].ValEnd:n.Kids[1].EntryStart], []byte("#")):
		sep = string(d.Src[n.Kids[0].ValEnd:n.Kids[1].EntryStart])
	case multiline:
		sep = "," + eol + li
	case tight:
		sep = ","
	}
	keySep := ": "
	if tight {
		keySep = ":"
	}
	var text string
	for _, e := range entries {
		s, err := toJSON(e.Val, pretty, tight, d.jsonUnit(), 0)
		if err != nil {
			return 0, "", err
		}
		if n.Val.Kind == yaml.MappingNode {
			s = jsonStr(e.Seg) + keySep + s
		}
		if pretty {
			s = strings.ReplaceAll(s, "\n", eol+li)
		}
		text += sep + s
	}
	return ref.ValEnd, text, nil
}

// insertBlock renders entries for a block container and returns where to
// splice them: on the lines after ref, indented like its siblings.
func (n *Node) insertBlock(t *newText, entries []*Node, ref *Node) (int, string, error) {
	d, eol := n.Doc, n.Doc.EOL
	lines := t.lines
	if isFlowColl(t.y()) { // flow input into a block container: one entry per line
		lines = nil
		for _, e := range entries {
			s, err := toJSON(e.Val, false, false, "  ", 0)
			if err != nil {
				return 0, "", err
			}
			if n.Val.Kind == yaml.MappingNode {
				lines = append(lines, yamlKey(e.Seg)+": "+s)
			} else {
				lines = append(lines, "- "+s)
			}
		}
	}
	c := d.col(n.Kids[0].EntryStart)
	at := d.lineEnd(ref.ValEnd)
	if at > 0 && d.Src[at-1] == '\r' {
		at--
	}
	return at, eol + joinPref(lines, sp(c), sp(c), eol), nil
}

func yamlKey(k string) string {
	if strings.Contains(k, "\n") {
		return jsonStr(k)
	}
	return yamlStr(k, 0)
}

// ---- rm ----

func (n *Node) rmEdit() ([]byte, error) {
	d, p := n.Doc, n.Parent
	if p == nil {
		return nil, fail(ExitUsage, "cannot remove the document root")
	}
	var next, prev *Node
	if n.Index+1 < len(p.Kids) {
		next = p.Kids[n.Index+1]
	}
	if n.Index > 0 {
		prev = p.Kids[n.Index-1]
	}
	var a, b int
	if isFlowColl(p.Val) {
		switch {
		case next != nil:
			a, b = n.EntryStart, next.EntryStart
		case prev != nil:
			a, b = prev.ValEnd, n.ValEnd
		default:
			a, b = n.EntryStart, n.ValEnd
		}
	} else {
		ls := d.lineStart(n.EntryStart)
		le := d.lineEnd(n.ValEnd)
		switch {
		case len(bytes.TrimSpace(d.Src[ls:n.EntryStart])) == 0: // entry owns its lines
			a, b = ls, min(le+1, len(d.Src))
		case next != nil: // first key after "- ": join the next one up
			a, b = n.EntryStart, next.EntryStart
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
