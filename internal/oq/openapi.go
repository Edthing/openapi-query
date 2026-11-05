package oq

import (
	"strings"

	"go.yaml.in/yaml/v3"
)

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
