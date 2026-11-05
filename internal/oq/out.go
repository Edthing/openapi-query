package oq

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

type KV struct {
	K string
	V any
}

type Rec []KV

// Tok is an approximate token count (characters / 4).
type Tok int

func tokensOf(s string) Tok { return Tok((utf8.RuneCountInString(s) + 3) / 4) }

func jsonStr(v any) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return strings.TrimRight(b.String(), "\n")
}

func (r Rec) json() string {
	var b strings.Builder
	b.WriteByte('{')
	for i, kv := range r {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(jsonStr(kv.K))
		b.WriteByte(':')
		switch v := kv.V.(type) {
		case Tok:
			b.WriteString(strconv.Itoa(int(v)))
		default:
			b.WriteString(jsonStr(v))
		}
	}
	b.WriteByte('}')
	return b.String()
}

// yaml renders the record as a block mapping; first is the prefix of the
// first line and rest the prefix of the others.
func (r Rec) yaml(first, rest string) string {
	var b strings.Builder
	for i, kv := range r {
		p := rest
		if i == 0 {
			p = first
		}
		var v string
		switch x := kv.V.(type) {
		case Tok:
			v = strconv.Itoa(int(x))
		case string:
			v = yamlStr(x, len(rest)+2)
		case map[string]int:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts := make([]string, len(keys))
			for j, k := range keys {
				parts[j] = k + ": " + strconv.Itoa(x[k])
			}
			v = "{" + strings.Join(parts, ", ") + "}"
		default:
			v = fmt.Sprint(x)
		}
		b.WriteString(p + kv.K + ": " + v + "\n")
	}
	return b.String()
}

// render formats records for the -o/--output option.
func render(recs []Rec, o *opts) ([]byte, error) {
	switch o.format {
	case "name":
		var b bytes.Buffer
		for _, r := range recs {
			if len(r) > 0 {
				b.WriteString(Rec{r[0]}.tsv() + "\n")
			}
		}
		return b.Bytes(), nil
	case "columns":
		var out []Rec
		for _, r := range recs {
			var p Rec
			for _, name := range o.columns {
				found := false
				for _, kv := range r {
					if kv.K == name {
						p = append(p, kv)
						found = true
						break
					}
				}
				if !found {
					var have []string
					for _, kv := range r {
						have = append(have, kv.K)
					}
					return nil, fail(ExitUsage, "unknown column %q (have: %s)", name, strings.Join(have, ", "))
				}
			}
			out = append(out, p)
		}
		return renderRecs(out, "", o.header), nil
	}
	return renderRecs(recs, o.format, o.header), nil
}

// oneOut renders a single metadata record in any format (tsv gives one row).
func oneOut(r Rec, o *opts) ([]byte, error) {
	if o.format == "" {
		return render([]Rec{r}, o)
	}
	return recOut(r, o)
}

// recOut renders one record (get, at, and -i results).
func recOut(r Rec, o *opts) ([]byte, error) {
	switch o.format {
	case "yaml":
		return []byte(r.yaml("", "")), nil
	case "name", "columns":
		return render([]Rec{r}, o)
	}
	return []byte(r.json() + "\n"), nil
}

var plainYAML = regexp.MustCompile(`^[A-Za-z_/][A-Za-z0-9_./~{}%-]*$`)

var yamlReserved = map[string]bool{"true": true, "false": true, "null": true, "yes": true, "no": true, "on": true, "off": true, "y": true, "n": true}

// yamlStr renders a string as YAML: plain when unambiguous, a literal block
// scalar for multi-line text (indented by ind), else a double-quoted scalar.
func yamlStr(s string, ind int) string {
	if s == "" {
		return `""`
	}
	if plainYAML.MatchString(s) && !yamlReserved[strings.ToLower(s)] {
		return s
	}
	if !strings.Contains(s, "\n") || strings.ContainsAny(s, "\r\x00\x1b\x7f") || !utf8.ValidString(s) {
		return jsonStr(s)
	}
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\t' {
			return jsonStr(s)
		}
	}
	hdr := "|"
	switch {
	case !strings.HasSuffix(s, "\n"):
		hdr += "-"
	case strings.HasSuffix(s, "\n\n"):
		hdr += "+"
	}
	if s[0] == ' ' || s[0] == '\n' { // leading space/blank line: explicit indentation indicator
		hdr = "|2" + hdr[1:]
	}
	pad := strings.Repeat(" ", ind)
	body := strings.TrimSuffix(s, "\n")
	if hdr[len(hdr)-1] == '+' {
		body = s[:len(s)-1]
	}
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return hdr + "\n" + strings.Join(lines, "\n")
}

func (r Rec) tsv() string {
	cols := make([]string, len(r))
	for i, kv := range r {
		switch v := kv.V.(type) {
		case Tok:
			cols[i] = "~" + strconv.Itoa(int(v))
		case string:
			cols[i] = strings.NewReplacer("\t", " ", "\n", " ", "\r", "").Replace(v)
		default:
			cols[i] = fmt.Sprint(v)
		}
	}
	return strings.Join(cols, "\t")
}

func renderRecs(recs []Rec, format string, header bool) []byte {
	var b bytes.Buffer
	if format == "yaml" {
		if len(recs) == 0 {
			return []byte("[]\n")
		}
		for _, r := range recs {
			b.WriteString(r.yaml("- ", "  "))
		}
		return b.Bytes()
	}
	if format == "json" {
		if len(recs) == 0 {
			return []byte("[]\n")
		}
		b.WriteString("[\n")
		for i, r := range recs {
			b.WriteString(r.json())
			if i < len(recs)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString("]\n")
		return b.Bytes()
	}
	if header && len(recs) > 0 {
		names := make([]string, len(recs[0]))
		for i, kv := range recs[0] {
			names[i] = kv.K
		}
		b.WriteString(strings.Join(names, "\t") + "\n")
	}
	for _, r := range recs {
		b.WriteString(r.tsv())
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func kindOf(y *yaml.Node) string {
	switch y.Kind {
	case yaml.MappingNode:
		return "object"
	case yaml.SequenceNode:
		return "array"
	case yaml.AliasNode:
		return "alias"
	}
	switch y.Tag {
	case "!!str":
		return "string"
	case "!!int", "!!float":
		return "number"
	case "!!bool":
		return "boolean"
	case "!!null":
		return "null"
	}
	return "scalar"
}

// DisplayText is the node's source text; block collections are dedented to
// the column of their first line so the result stands alone.
func (n *Node) DisplayText() string {
	t := n.Text()
	if !strings.Contains(t, "\n") {
		return t
	}
	c := n.Doc.col(n.EntryStart)
	if isColl(n.Val) && !isFlowColl(n.Val) {
		c = n.Doc.col(n.ValStart)
		if len(n.Kids) > 0 {
			c = n.Doc.col(n.Kids[0].EntryStart)
		}
	}
	if c > 0 {
		lines := strings.Split(t, "\n")
		for i := 1; i < len(lines); i++ {
			lines[i] = stripIndent(lines[i], c)
		}
		t = strings.Join(lines, "\n")
	}
	return t
}

func stripIndent(l string, c int) string {
	i := 0
	for i < len(l) && i < c && (l[i] == ' ' || l[i] == '\t') {
		i++
	}
	return l[i:]
}

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:12]
}

func (n *Node) note() string {
	switch {
	case n.Val.Kind == yaml.AliasNode:
		return "*" + n.Val.Value
	case n.Val.Anchor != "":
		return "&" + n.Val.Anchor
	}
	return ""
}

func (n *Node) lines() (int, int) {
	a, _ := n.Doc.Pos(n.EntryStart)
	b, _ := n.Doc.Pos(n.ValEnd)
	return a, b
}
