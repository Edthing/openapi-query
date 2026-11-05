package oq

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// Doc is one parsed YAML or JSON file with byte-exact node ranges.
type Doc struct {
	Abs     string // absolute path; "" for stdin
	Disp    string // path shown in addresses; "" for the root spec
	Src     []byte
	Root    *Node
	EOL     string
	JSONish bool
	lines   []int
	unit    int
}

// Node is an addressable value. For mapping children it also covers the key.
type Node struct {
	Doc    *Doc
	Parent *Node
	Seg    string
	Key    *yaml.Node
	Val    *yaml.Node
	Kids   []*Node

	EntryStart int // start of key (mapping), dash (block sequence) or value
	ValStart   int
	ValEnd     int
	AfterColon int // mapping children only
}

func ParseDoc(abs, disp string, src []byte) (*Doc, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	var top yaml.Node
	if err := dec.Decode(&top); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("empty document")
		}
		return nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, errors.New("multiple YAML documents are not supported")
	} else if !errors.Is(err, io.EOF) {
		return nil, err
	}
	if top.Kind != yaml.DocumentNode || len(top.Content) == 0 {
		return nil, errors.New("empty document")
	}
	d := &Doc{Abs: abs, Disp: disp, Src: src, EOL: "\n"}
	if bytes.Contains(src, []byte("\r\n")) {
		d.EOL = "\r\n"
	}
	d.lines = append(d.lines, 0)
	for i, c := range src {
		if c == '\n' {
			d.lines = append(d.lines, i+1)
		}
	}
	y := top.Content[0]
	root := &Node{Doc: d, Val: y}
	root.ValStart = d.off(y.Line, y.Column)
	root.EntryStart = root.ValStart
	d.Root = root
	d.expand(root, len(src))
	t := bytes.TrimSpace(src)
	d.JSONish = len(t) > 0 && (t[0] == '{' || t[0] == '[') && y.Style&yaml.FlowStyle != 0 && jsonKeys(y)
	return d, nil
}

func jsonKeys(y *yaml.Node) bool {
	if y.Kind == yaml.MappingNode && len(y.Content) > 0 {
		return y.Content[0].Style&yaml.DoubleQuotedStyle != 0
	}
	return true
}

// off converts a 1-based line and character column to a byte offset.
func (d *Doc) off(line, col int) int {
	if line < 1 {
		return 0
	}
	if line > len(d.lines) {
		return len(d.Src)
	}
	p := d.lines[line-1]
	for c := 1; c < col && p < len(d.Src) && d.Src[p] != '\n'; c++ {
		_, w := utf8.DecodeRune(d.Src[p:])
		p += w
	}
	return p
}

// Pos returns the 1-based line and character column of a byte offset.
func (d *Doc) Pos(p int) (line, col int) {
	if p > len(d.Src) {
		p = len(d.Src)
	}
	lo, hi := 0, len(d.lines)
	for lo < hi-1 {
		m := (lo + hi) / 2
		if d.lines[m] <= p {
			lo = m
		} else {
			hi = m
		}
	}
	return lo + 1, utf8.RuneCount(d.Src[d.lines[lo]:p]) + 1
}

func (d *Doc) lineStart(p int) int {
	l, _ := d.Pos(p)
	return d.lines[l-1]
}

func (d *Doc) lineEnd(p int) int { // index of '\n' or len
	i := bytes.IndexByte(d.Src[p:], '\n')
	if i < 0 {
		return len(d.Src)
	}
	return p + i
}

func (d *Doc) col(p int) int { _, c := d.Pos(p); return c - 1 }

func isFlowColl(y *yaml.Node) bool {
	return (y.Kind == yaml.MappingNode || y.Kind == yaml.SequenceNode) && y.Style&yaml.FlowStyle != 0
}

func isColl(y *yaml.Node) bool { return y.Kind == yaml.MappingNode || y.Kind == yaml.SequenceNode }

func (n *Node) flowParent() bool { return n.Parent != nil && isFlowColl(n.Parent.Val) }

func (n *Node) Text() string { return string(n.Doc.Src[n.ValStart:n.ValEnd]) }

func (n *Node) Segs() []string {
	var s []string
	for m := n; m.Parent != nil; m = m.Parent {
		s = append(s, m.Seg)
	}
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	return s
}

func (n *Node) Child(seg string) *Node {
	for _, k := range n.Kids {
		if k.Seg == seg {
			return k
		}
	}
	return nil
}

// expand creates children and computes ends. bound is the exclusive limit
// (next sibling's start or the parent's limit).
func (d *Doc) expand(n *Node, bound int) {
	y := n.Val
	flow := isFlowColl(y)
	if flow {
		n.ValEnd = matchClose(d.Src, n.ValStart)
	}
	switch y.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(y.Content); i += 2 {
			k, v := y.Content[i], y.Content[i+1]
			kid := &Node{Doc: d, Parent: n, Seg: k.Value, Key: k, Val: v}
			kid.EntryStart = d.off(k.Line, k.Column)
			kid.ValStart = d.off(v.Line, v.Column)
			kid.AfterColon = d.colonAfter(k, kid.EntryStart, kid.ValStart)
			n.Kids = append(n.Kids, kid)
		}
	case yaml.SequenceNode:
		for i, v := range y.Content {
			kid := &Node{Doc: d, Parent: n, Seg: strconv.Itoa(i), Val: v}
			kid.ValStart = d.off(v.Line, v.Column)
			kid.EntryStart = kid.ValStart
			if !flow {
				kid.EntryStart = d.dashBefore(kid.ValStart)
			}
			n.Kids = append(n.Kids, kid)
		}
	}
	limit := bound
	if flow {
		limit = n.ValEnd - 1
	}
	for i, kid := range n.Kids {
		b := limit
		if i+1 < len(n.Kids) {
			b = n.Kids[i+1].EntryStart
		}
		d.expand(kid, b)
	}
	if flow {
		return
	}
	if isColl(y) && len(n.Kids) > 0 {
		n.ValEnd = n.Kids[len(n.Kids)-1].ValEnd
		return
	}
	n.ValEnd = d.scalarEnd(n, bound)
}

func (d *Doc) dashBefore(p int) int {
	i := p
	for i > 0 {
		switch d.Src[i-1] {
		case ' ', '\t', '\n', '\r':
			i--
			continue
		case '-':
			return i - 1
		}
		break
	}
	return p
}

// colonAfter returns the offset just past the ':' following a mapping key.
func (d *Doc) colonAfter(k *yaml.Node, ks, vs int) int {
	p := ks
	src := d.Src
	switch {
	case k.Style&yaml.DoubleQuotedStyle != 0 || k.Style&yaml.SingleQuotedStyle != 0:
		p = quotedEnd(src, ks)
	default:
		for p < len(src) {
			c := src[p]
			if c == ':' && (p+1 >= len(src) || isSpace(src[p+1]) || src[p+1] == ',' || src[p+1] == '}' || src[p+1] == ']') {
				return p + 1
			}
			if c == '\n' {
				break
			}
			p++
		}
		return vs
	}
	for p < len(src) && (src[p] == ' ' || src[p] == '\t') {
		p++
	}
	if p < len(src) && src[p] == ':' {
		return p + 1
	}
	return vs
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// quotedEnd returns the offset just past the closing quote of the scalar at s.
func quotedEnd(src []byte, s int) int {
	q := src[s]
	for i := s + 1; i < len(src); i++ {
		switch {
		case q == '"' && src[i] == '\\':
			i++
		case src[i] == q:
			if q == '\'' && i+1 < len(src) && src[i+1] == '\'' {
				i++
				continue
			}
			return i + 1
		}
	}
	return len(src)
}

// matchClose returns the offset just past the bracket matching src[s].
func matchClose(src []byte, s int) int {
	depth := 0
	for i := s; i < len(src); i++ {
		switch src[i] {
		case '"', '\'':
			i = quotedEnd(src, i) - 1
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		case '#':
			if i > 0 && isSpace(src[i-1]) {
				for i < len(src) && src[i] != '\n' {
					i++
				}
			}
		}
	}
	return len(src)
}

func (d *Doc) scalarEnd(n *Node, bound int) int {
	y, s, src := n.Val, n.ValStart, d.Src
	if s >= len(src) {
		return len(src)
	}
	switch {
	case y.Kind == yaml.AliasNode:
		i := s
		for i < len(src) && !isSpace(src[i]) && src[i] != ',' && src[i] != ']' && src[i] != '}' {
			i++
		}
		return i
	case y.Tag == "!!null" && y.Value == "" && y.Style == 0 && y.Anchor == "":
		return s
	}
	i := s
	// skip anchor / tag properties
	for i < len(src) && (src[i] == '&' || src[i] == '!') {
		for i < len(src) && !isSpace(src[i]) {
			i++
		}
		for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
			i++
		}
	}
	if i >= len(src) {
		return len(src)
	}
	switch {
	case y.Style&yaml.DoubleQuotedStyle != 0 || y.Style&yaml.SingleQuotedStyle != 0:
		return quotedEnd(src, i)
	case y.Style&yaml.LiteralStyle != 0 || y.Style&yaml.FoldedStyle != 0:
		return d.blockScalarEnd(n, i, bound)
	}
	// plain scalar, possibly multi-line, ends before the next sibling
	e := bound
	if e > len(src) {
		e = len(src)
	}
	last := i
	for p := i; p < e; p++ {
		c := src[p]
		if c == '#' && p > i && isSpace(src[p-1]) {
			for p < e && src[p] != '\n' {
				p++
			}
			continue
		}
		if n.flowParent() && (c == ',' || c == '}' || c == ']') {
			break
		}
		if !isSpace(c) {
			last = p + 1
		}
	}
	return last
}

func (d *Doc) blockScalarEnd(n *Node, hdr, bound int) int {
	src := d.Src
	parentIndent := -1
	if n.Parent != nil {
		parentIndent = d.col(n.EntryStart)
	}
	p := d.lineEnd(hdr)
	end := p
	content := -1
	keep := false
	if f := bytes.Fields(src[hdr:p]); len(f) > 0 {
		keep = bytes.IndexByte(f[0], '+') >= 0
		for _, ch := range f[0] {
			if ch >= '1' && ch <= '9' { // explicit indentation indicator
				content = max(parentIndent, 0) + int(ch-'0')
			}
		}
	}
	for p < len(src) {
		ls := p + 1
		if ls > len(src) {
			break
		}
		le := d.lineEnd(ls)
		line := src[ls:le]
		ind := 0
		for ind < len(line) && line[ind] == ' ' {
			ind++
		}
		blank := len(bytes.TrimSpace(line)) == 0
		if blank {
			p = le
			if keep && content >= 0 {
				end = le
			}
			continue
		}
		if content < 0 {
			if ind <= parentIndent {
				break
			}
			content = ind
		} else if ind < content {
			break
		}
		end = le
		p = le
	}
	for end > hdr && (src[end-1] == '\r') {
		end--
	}
	return end
}
