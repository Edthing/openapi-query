package oq

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
