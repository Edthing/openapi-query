package oq

import (
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

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
	key := refKey{from, raw}
	if t, ok := w.refCache[key]; ok {
		return t
	}
	t := w.parseRef(from, raw)
	if w.refCache == nil {
		w.refCache = map[refKey]RefTarget{}
	}
	w.refCache[key] = t
	return t
}

type refKey struct {
	from *Doc
	raw  string
}

func (w *WS) parseRef(from *Doc, raw string) RefTarget {
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

// TargetAddr renders the target as an oq address (the raw ref if it has no document).
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
		for _, k := range n.Kids { // no Child(): that would index every large map
			if k.Seg == "$ref" {
				if k.Val.Kind == yaml.ScalarNode && k.Val.Tag == "!!str" {
					*out = append(*out, RefSite{Node: n, Raw: k.Val.Value})
				}
				break
			}
		}
	}
	for _, k := range n.Kids {
		refSites(k, out)
	}
}

// allRefs lists every $ref site in the document, computed once.
func (d *Doc) allRefs() []RefSite {
	if !d.sitesOK {
		refSites(d.Root, &d.sites)
		d.sitesOK = true
	}
	return d.sites
}

// Closure loads every file reachable via $ref from the root and returns docs
// in discovery order (root first).
func (w *WS) Closure() []*Doc {
	docs := []*Doc{w.Root}
	seen := map[*Doc]bool{w.Root: true}
	for i := 0; i < len(docs); i++ {
		files := map[string]bool{} // only the first ref to each file needs loading
		for _, s := range docs[i].allRefs() {
			file, _, _ := strings.Cut(s.Raw, "#")
			if file == "" || files[file] {
				continue
			}
			files[file] = true
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
