package oq

import (
	"strings"

	"go.yaml.in/yaml/v3"
)

// ---- OpenAPI structure ----

func (w *WS) Version() string {
	for _, k := range []string{"openapi", "swagger"} {
		if v, ok := w.Root.Root.ScalarChild(k); ok {
			return v
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
	if !w.opsOK {
		w.ops, w.opsOK = w.listOps(), true
	}
	return w.ops
}

func (w *WS) listOps() []Op {
	var ops []Op
	paths := w.Root.Root.Child("paths")
	if paths == nil {
		return nil
	}
	for _, pi := range paths.Kids {
		item := pi
		if ref, ok := item.ScalarChild("$ref"); ok {
			if t := w.ParseRef(item.Doc, ref); t.Node != nil {
				item = t.Node
			}
		}
		for _, m := range methods {
			op := item.Child(m)
			if op == nil || op.Val.Kind != yaml.MappingNode {
				continue
			}
			id := ""
			id, _ = op.ScalarChild("operationId")
			ops = append(ops, Op{Node: op, Method: m, Path: pi.Seg, ID: id})
		}
	}
	return ops
}
