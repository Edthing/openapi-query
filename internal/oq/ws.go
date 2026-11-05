package oq

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Exit codes.
const (
	ExitOK       = 0
	ExitNotFound = 1
	ExitUsage    = 2
	ExitStale    = 3
	ExitCap      = 4
)

type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

func fail(code int, format string, a ...any) error {
	return &ExitError{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// WS is the set of files reachable from one root spec.
type WS struct {
	Base string
	Root *Doc
	docs map[string]*Doc
	errs map[string]error
}

func NewWS(path string, stdin []byte) (*WS, error) {
	w := &WS{docs: map[string]*Doc{}, errs: map[string]error{}}
	cwd, _ := os.Getwd()
	var src []byte
	var abs string
	if path == "-" {
		w.Base = cwd
		src = stdin
	} else {
		a, err := filepath.Abs(path)
		if err != nil {
			return nil, fail(ExitUsage, "%v", err)
		}
		abs = a
		w.Base = filepath.Dir(a)
		src, err = os.ReadFile(a)
		if err != nil {
			return nil, fail(ExitUsage, "cannot read %s: %v", path, errors.Unwrap(err))
		}
	}
	d, err := ParseDoc(abs, "", src)
	if err != nil {
		return nil, fail(ExitUsage, "%s: parse error: %v", path, err)
	}
	w.Root = d
	if abs != "" {
		w.docs[abs] = d
	}
	return w, nil
}

func (w *WS) Load(abs string) (*Doc, error) {
	if w.Root.Abs == abs {
		return w.Root, nil
	}
	if d, ok := w.docs[abs]; ok {
		return d, nil
	}
	if e, ok := w.errs[abs]; ok {
		return nil, e
	}
	src, err := os.ReadFile(abs)
	if err != nil {
		e := fmt.Errorf("file not found: %s", w.rel(abs))
		w.errs[abs] = e
		return nil, e
	}
	disp := w.rel(abs)
	d, err := ParseDoc(abs, disp, src)
	if err != nil {
		e := fmt.Errorf("%s: parse error: %v", disp, err)
		w.errs[abs] = e
		return nil, e
	}
	w.docs[abs] = d
	return d, nil
}

func (w *WS) rel(abs string) string {
	r, err := filepath.Rel(w.Base, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(r)
}

func (w *WS) dirOf(d *Doc) string {
	if d.Abs == "" {
		return w.Base
	}
	return filepath.Dir(d.Abs)
}

// ---- pointer encoding ----

func escSeg(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= 0x20 || c == 0x7f || c == '%' || c == '\'' || c == '"' || c == '\\' {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func unescSeg(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if h, l := unhex(s[i+1]), unhex(s[i+2]); h >= 0 && l >= 0 {
				b.WriteByte(byte(h<<4 | l))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	s = b.String()
	s = strings.ReplaceAll(s, "~1", "/")
	return strings.ReplaceAll(s, "~0", "~")
}

func parsePointer(p string) []string {
	p = strings.TrimPrefix(p, "#")
	if p == "" || p == "/" {
		return nil
	}
	parts := strings.Split(p[1:], "/")
	for i := range parts {
		parts[i] = unescSeg(parts[i])
	}
	return parts
}

func ptrString(segs []string) string {
	if len(segs) == 0 {
		return "/"
	}
	var b strings.Builder
	for _, s := range segs {
		b.WriteByte('/')
		b.WriteString(escSeg(s))
	}
	return b.String()
}

// Addr is the canonical address of a node as printed by oq.
func (w *WS) Addr(n *Node) string { return w.AddrOf(n.Doc, n.Segs()) }

func (w *WS) AddrOf(d *Doc, segs []string) string {
	if d.Disp == "" {
		return ptrString(segs)
	}
	return d.Disp + "#" + ptrString(segs)
}

// Descend follows segments from a node without following refs.
func (n *Node) Descend(segs []string) *Node {
	cur := n
	for _, s := range segs {
		if cur = cur.Child(s); cur == nil {
			return nil
		}
	}
	return cur
}

// ---- addresses ----

var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

func isMethod(s string) bool {
	s = strings.ToLower(s)
	for _, m := range methods {
		if m == s {
			return true
		}
	}
	return false
}

func (w *WS) Resolve(addr string) (*Node, error) {
	a := strings.TrimSpace(addr)
	switch {
	case strings.HasPrefix(a, "opid:"):
		id := strings.TrimPrefix(a, "opid:")
		for _, op := range w.Ops() {
			if op.ID == id {
				return op.Node, nil
			}
		}
		return nil, fail(ExitNotFound, "no operation with operationId %q", id)
	case strings.HasPrefix(a, "path:"):
		p := strings.TrimPrefix(a, "path:")
		if paths := w.Root.Root.Child("paths"); paths != nil {
			if n := paths.Child(p); n != nil {
				return n, nil
			}
		}
		return nil, fail(ExitNotFound, "no path %q", p)
	case strings.HasPrefix(a, "schema:"):
		name := strings.TrimPrefix(a, "schema:")
		for _, base := range w.schemaBases() {
			if n := base.Child(name); n != nil {
				return n, nil
			}
		}
		return nil, fail(ExitNotFound, "no schema named %q", name)
	}
	if i := strings.IndexByte(a, ' '); i > 0 && isMethod(a[:i]) && strings.HasPrefix(strings.TrimSpace(a[i+1:]), "/") {
		m, p := strings.ToLower(a[:i]), strings.TrimSpace(a[i+1:])
		for _, op := range w.Ops() {
			if op.Method == m && op.Path == p {
				return op.Node, nil
			}
		}
		return nil, fail(ExitNotFound, "no operation %s %s", strings.ToUpper(m), p)
	}
	if a == "" || a == "/" || a == "#" || a == "#/" {
		return w.Root.Root, nil
	}
	if strings.HasPrefix(a, "#") || (strings.HasPrefix(a, "/") && !w.looksLikeFile(a)) {
		return w.resolvePtr(w.Root, parsePointer(a), a)
	}
	file, frag := a, ""
	if i := strings.IndexByte(a, '#'); i >= 0 {
		file, frag = a[:i], a[i:]
	}
	abs := file
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(w.Base, file)
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fail(ExitNotFound, "address %q: no such file or JSON pointer", a)
	}
	d, err := w.Load(abs)
	if err != nil {
		return nil, fail(ExitUsage, "%v", err)
	}
	return w.resolvePtr(d, parsePointer(frag), a)
}

func (w *WS) looksLikeFile(a string) bool {
	i := strings.IndexByte(a, '#')
	if i <= 0 {
		return false
	}
	_, err := os.Stat(a[:i])
	return err == nil
}

func (w *WS) resolvePtr(d *Doc, segs []string, shown string) (*Node, error) {
	cur := d.Root
	for i, s := range segs {
		next := cur.Child(s)
		if next == nil {
			return nil, fail(ExitNotFound, "address not found: %s%s", shown, hintKids(cur, s, segs[:i]))
		}
		cur = next
	}
	return cur, nil
}

func hintKids(n *Node, missing string, at []string) string {
	if len(n.Kids) == 0 {
		return fmt.Sprintf(" (%s has no children)", ptrString(at))
	}
	var keys []string
	for i, k := range n.Kids {
		if i == 8 {
			keys = append(keys, "...")
			break
		}
		keys = append(keys, k.Seg)
	}
	return fmt.Sprintf(" (no %q under %s; has: %s)", missing, ptrString(at), strings.Join(keys, ", "))
}

// ---- OpenAPI structure ----

func (w *WS) Version() string {
	for _, k := range []string{"openapi", "swagger"} {
		if n := w.Root.Root.Child(k); n != nil && n.Val.Kind == yaml.ScalarNode {
			return n.Val.Value
		}
	}
	return ""
}

func (w *WS) schemaBases() []*Node {
	var out []*Node
	r := w.Root.Root
	if w.Version() != "" && strings.HasPrefix(w.Version(), "2") {
		if n := r.Descend([]string{"definitions"}); n != nil {
			out = append(out, n)
		}
		return out
	}
	if n := r.Descend([]string{"components", "schemas"}); n != nil {
		out = append(out, n)
	}
	if len(out) == 0 {
		if n := r.Descend([]string{"definitions"}); n != nil {
			out = append(out, n)
		}
	}
	return out
}

type Op struct {
	Node   *Node
	Method string // lower case
	Path   string
	ID     string
}

// Ops lists operations in document order, following path item $refs.
func (w *WS) Ops() []Op {
	var ops []Op
	paths := w.Root.Root.Child("paths")
	if paths == nil {
		return nil
	}
	for _, pi := range paths.Kids {
		item := pi
		if r := item.Child("$ref"); r != nil && r.Val.Kind == yaml.ScalarNode {
			if t := w.ParseRef(item.Doc, r.Val.Value); t.Node != nil {
				item = t.Node
			}
		}
		for _, m := range methods {
			op := item.Child(m)
			if op == nil || op.Val.Kind != yaml.MappingNode {
				continue
			}
			id := ""
			if o := op.Child("operationId"); o != nil && o.Val.Kind == yaml.ScalarNode {
				id = o.Val.Value
			}
			ops = append(ops, Op{Node: op, Method: m, Path: pi.Seg, ID: id})
		}
	}
	return ops
}

// ---- refs ----

type RefTarget struct {
	Raw    string
	Doc    *Doc
	Segs   []string
	Node   *Node
	Remote bool
	Reason string // why unresolved
}

func (t RefTarget) Resolved() bool { return t.Node != nil }

func (w *WS) ParseRef(from *Doc, raw string) RefTarget {
	t := RefTarget{Raw: raw}
	file, frag := raw, ""
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		file, frag = raw[:i], raw[i+1:]
	}
	d := from
	if file != "" {
		if strings.Contains(file, "://") {
			t.Remote = true
			t.Reason = "remote ref not followed"
			return t
		}
		abs := file
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(w.dirOf(from), file)
		}
		var err error
		if d, err = w.Load(abs); err != nil {
			t.Reason = err.Error()
			return t
		}
	}
	t.Doc = d
	if frag != "" && !strings.HasPrefix(frag, "/") {
		t.Reason = "fragment is not a JSON pointer"
		return t
	}
	t.Segs = parsePointer("#" + frag)
	t.Node = d.Root.Descend(t.Segs)
	if t.Node == nil {
		t.Reason = "pointer not found"
	}
	return t
}

// Address renders the target as an oq address (raw text if there is no document).
func (w *WS) TargetAddr(t RefTarget) string {
	if t.Doc == nil {
		return t.Raw
	}
	return w.AddrOf(t.Doc, t.Segs)
}

type RefSite struct {
	Node *Node // the node holding "$ref"
	Raw  string
}

// refSites collects every "$ref": "<string>" under n (not following aliases).
func refSites(n *Node, out *[]RefSite) {
	if n.Val.Kind == yaml.MappingNode {
		if r := n.Child("$ref"); r != nil && r.Val.Kind == yaml.ScalarNode && r.Val.Tag == "!!str" {
			*out = append(*out, RefSite{Node: n, Raw: r.Val.Value})
		}
	}
	for _, k := range n.Kids {
		refSites(k, out)
	}
}

// Closure loads every file reachable via $ref from the root and returns docs
// in discovery order (root first).
func (w *WS) Closure() []*Doc {
	docs := []*Doc{w.Root}
	seen := map[*Doc]bool{w.Root: true}
	for i := 0; i < len(docs); i++ {
		var sites []RefSite
		refSites(docs[i].Root, &sites)
		for _, s := range sites {
			t := w.ParseRef(docs[i], s.Raw)
			if t.Doc != nil && !seen[t.Doc] {
				seen[t.Doc] = true
				docs = append(docs, t.Doc)
			}
		}
	}
	return docs
}

// MissingFiles returns referenced files that failed to load, sorted.
func (w *WS) MissingFiles() map[string]error {
	out := map[string]error{}
	for abs, e := range w.errs {
		out[w.rel(abs)] = e
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	k := make([]string, 0, len(m))
	for s := range m {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}

func itoa(i int) string { return strconv.Itoa(i) }
