package oq

import (
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

var jsonNum = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// toJSON renders a node as JSON; pretty output indents by unit at level.
func toJSON(y *yaml.Node, pretty, tight bool, unit string, level int) (string, error) {
	in := func(l int) string { return strings.Repeat(unit, l) }
	switch y.Kind {
	case yaml.MappingNode, yaml.SequenceNode:
		isMap := y.Kind == yaml.MappingNode
		open, cl := "[", "]"
		if isMap {
			open, cl = "{", "}"
		}
		if len(y.Content) == 0 {
			return open + cl, nil
		}
		var parts []string
		step := 1
		if isMap {
			step = 2
		}
		for i := 0; i < len(y.Content); i += step {
			v := y.Content[i+step-1]
			s, err := toJSON(v, pretty, tight, unit, level+1)
			if err != nil {
				return "", err
			}
			if isMap {
				sepc := ": "
				if tight {
					sepc = ":"
				}
				s = jsonStr(y.Content[i].Value) + sepc + s
			}
			parts = append(parts, s)
		}
		if pretty {
			var b strings.Builder
			b.WriteString(open + "\n")
			for i, p := range parts {
				b.WriteString(in(level+1) + p)
				if i < len(parts)-1 {
					b.WriteString(",")
				}
				b.WriteString("\n")
			}
			b.WriteString(in(level) + cl)
			return b.String(), nil
		}
		j := ", "
		if tight {
			j = ","
		}
		return open + strings.Join(parts, j) + cl, nil
	case yaml.AliasNode:
		return "", fail(ExitUsage, "aliases (*%s) cannot be written into JSON or flow context", y.Value)
	}
	switch y.Tag {
	case "!!null":
		return "null", nil
	case "!!bool":
		return strings.ToLower(y.Value), nil
	case "!!int", "!!float":
		if jsonNum.MatchString(y.Value) {
			return y.Value, nil
		}
	}
	return jsonStr(y.Value), nil
}

// flowText renders the new content for a JSON/flow context at node n.
func (t *newText) flowText(n *Node) (string, error) {
	d := n.Doc
	pretty := d.JSONish && d.pretty()
	tight := d.JSONish && !pretty
	s, err := toJSON(t.y(), pretty, tight, d.jsonUnit(), 0)
	if err != nil {
		return "", err
	}
	if pretty {
		s = strings.ReplaceAll(s, "\n", d.EOL+d.lineIndent(n.EntryStart))
	}
	return s, nil
}
