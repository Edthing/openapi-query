package oq

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func hasAlias(y *yaml.Node) bool {
	if y.Kind == yaml.AliasNode {
		return true
	}
	for _, c := range y.Content {
		if hasAlias(c) {
			return true
		}
	}
	return false
}

// TestCorpus checks, for every spec in $OQ_CORPUS, that each node's slice
// parses back to the parser's node and that writing a node back unchanged
// leaves the file byte-identical.
func TestCorpus(t *testing.T) {
	dir := os.Getenv("OQ_CORPUS")
	if dir == "" {
		t.Skip("set OQ_CORPUS to a directory of specs")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	limit := 1 << 30
	if os.Getenv("OQ_CORPUS_MAX_BYTES") != "" {
		limit = 2_000_000
	}
	rtEvery := 1
	if os.Getenv("OQ_CORPUS_RT_EVERY") != "" {
		fmt.Sscan(os.Getenv("OQ_CORPUS_RT_EVERY"), &rtEvery)
	}
	rtN := 0
	var nodes, skipped, bad, rtBad, files_ int
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil || len(src) > limit {
			continue
		}
		d, err := ParseDoc("", "", src)
		if err != nil {
			continue
		}
		files_++
		var walk func(n *Node, depth int)
		walk = func(n *Node, depth int) {
			if n.Parent != nil {
				nodes++
				if hasAlias(n.Val) || n.Val.Anchor != "" {
					skipped++
				} else if tx := strings.TrimSpace(n.Text()); tx == "..." || tx == "---" {
					skipped++ // a bare document marker cannot be parsed standalone
				} else if n.ValStart != n.ValEnd {
					txt := n.DisplayText()
					var y yaml.Node
					err := yaml.Unmarshal([]byte(txt+"\n"), &y)
					if err != nil || len(y.Content) == 0 || !equalNode(y.Content[0], n.Val) {
						bad++
						if bad <= 15 {
							t.Errorf("%s %s: slice mismatch (err=%v)\n---\n%.200s", filepath.Base(f), ptrString(n.Segs()), err, txt)
						}
					}
				}
				rtN++
				if depth <= 3 && rtN%rtEvery == 0 && n.Val.Anchor == "" && n.ValStart != n.ValEnd && !hasAlias(n.Val) {
					nt, err := prepText([]byte(n.DisplayText()))
					if err == nil && !strings.HasPrefix(strings.TrimSpace(n.DisplayText()), "|") && !hasKeepScalar(n) {
						out, err := n.setEdit(nt)
						if err != nil || !bytes.Equal(out, src) {
							rtBad++
							if rtBad <= 15 {
								t.Errorf("%s %s: unchanged write differs (err=%v)", filepath.Base(f), ptrString(n.Segs()), err)
							}
						}
					}
				}
			}
			for _, k := range n.Kids {
				walk(k, depth+1)
			}
		}
		walk(d.Root, 0)
	}
	t.Logf("files=%d nodes=%d skipped(alias/anchor)=%d slice-mismatch=%d roundtrip-diff=%d", files_, nodes, skipped, bad, rtBad)
}

var keepHeader = regexp.MustCompile(`[|>][0-9]?\+`)

// hasKeepScalar reports a |+ / >+ block scalar in the node's text, whose
// trailing blank lines oq does not round-trip (documented in the README).
func hasKeepScalar(n *Node) bool { return keepHeader.MatchString(n.Text()) }
