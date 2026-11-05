package oq

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// ---- command plumbing ----

type edited struct {
	doc  *Doc
	out  []byte
	segs []string // node whose hash is reported with -o json
}

func checkExpect(expect string, n *Node, w *WS) error {
	if expect == "" {
		return nil
	}
	e := strings.ToLower(expect)
	if len(e) < 6 {
		return fail(ExitUsage, "--expect needs at least 6 hex characters of the hash from 'oq get -o columns=hash'")
	}
	h := hashOf(n.DisplayText())
	if !strings.HasPrefix(h, e) {
		return fail(ExitStale, "stale: %s has hash %s, expected %s", w.Addr(n), h, expect)
	}
	return nil
}

func (c *cli) readNew(o *opts, spec string) (*newText, error) {
	var raw []byte
	var err error
	switch {
	case o.file != "":
		raw, err = os.ReadFile(o.file)
	case spec == "-":
		return nil, fail(ExitUsage, "the spec is on stdin; pass the new content with --file")
	default:
		raw, err = c.stdin()
	}
	if err != nil {
		return nil, fail(ExitUsage, "reading new content: %v", err)
	}
	return prepText(raw)
}

func (c *cli) finish(o *opts, spec string, apply func() (*edited, error)) (*result, error) {
	e, err := apply()
	if err != nil {
		return nil, err
	}
	if !o.inPlace {
		if o.format != "" {
			return nil, fail(ExitUsage, "-o on a write command needs -i")
		}
		return &result{data: e.out}, nil
	}
	if e.doc.Abs == "" {
		return nil, fail(ExitUsage, "-i needs a file, not stdin")
	}
	path := e.doc.Abs
	if r, err := filepath.EvalSymlinks(path); err == nil {
		path = r
	}
	unlock := lockDir(filepath.Dir(path))
	defer unlock()
	for try := 0; try < 5; try++ {
		if try > 0 {
			if e, err = apply(); err != nil {
				return nil, err
			}
		}
		tmp, err := os.CreateTemp(filepath.Dir(path), ".oq-*")
		if err != nil {
			return nil, fail(ExitUsage, "%v", err)
		}
		if st, err := os.Stat(path); err == nil {
			tmp.Chmod(st.Mode().Perm())
		}
		_, werr := tmp.Write(e.out)
		if werr == nil {
			werr = tmp.Sync()
		}
		tmp.Close()
		if werr != nil {
			os.Remove(tmp.Name())
			return nil, fail(ExitUsage, "%v", werr)
		}
		cur, _ := os.ReadFile(path)
		if !bytes.Equal(cur, e.doc.Src) {
			os.Remove(tmp.Name())
			continue
		}
		if err := os.Rename(tmp.Name(), path); err != nil {
			os.Remove(tmp.Name())
			return nil, fail(ExitUsage, "%v", err)
		}
		if o.format != "" {
			nd, err := ParseDoc("", "", e.out)
			if err != nil {
				return nil, fail(ExitUsage, "%v", err)
			}
			h := ""
			if m := nd.Root.Descend(e.segs); m != nil {
				h = hashOf(m.DisplayText())
			}
			rec := Rec{{"address", ptrString(e.segs)}, {"file", filepath.Base(path)}, {"hash", h}}
			data, err := recOut(rec, o)
			if err != nil {
				return nil, err
			}
			return &result{data: data}, nil
		}
		return nil, nil
	}
	return nil, fail(ExitStale, "stale: %s kept changing while writing", filepath.Base(path))
}

func cmdSet(c *cli, o *opts) (*result, error) {
	if err := need(o, 2, 2, "oq set <spec> <addr> [--expect H] [-i]"); err != nil {
		return nil, err
	}
	t, err := c.readNew(o, o.pos[0])
	if err != nil {
		return nil, err
	}
	return c.finish(o, o.pos[0], func() (*edited, error) {
		w, err := c.workspace(o.pos[0])
		if err != nil {
			return nil, err
		}
		n, err := w.Resolve(o.pos[1])
		if err != nil {
			return nil, err
		}
		if err := checkExpect(o.expect, n, w); err != nil {
			return nil, err
		}
		out, err := n.setEdit(t)
		if err != nil {
			return nil, err
		}
		return &edited{n.Doc, out, n.Segs()}, nil
	})
}

func cmdInsert(c *cli, o *opts) (*result, error) {
	if err := need(o, 2, 2, "oq insert <spec> <addr> [--after KEY] [-i]"); err != nil {
		return nil, err
	}
	t, err := c.readNew(o, o.pos[0])
	if err != nil {
		return nil, err
	}
	return c.finish(o, o.pos[0], func() (*edited, error) {
		w, err := c.workspace(o.pos[0])
		if err != nil {
			return nil, err
		}
		n, err := w.Resolve(o.pos[1])
		if err != nil {
			return nil, err
		}
		if err := checkExpect(o.expect, n, w); err != nil {
			return nil, err
		}
		out, err := n.insertEdit(t, o.after, o.afterSet)
		if err != nil {
			return nil, err
		}
		return &edited{n.Doc, out, n.Segs()}, nil
	})
}

func cmdRm(c *cli, o *opts) (*result, error) {
	if err := need(o, 2, 2, "oq rm <spec> <addr> [--expect H] [-i]"); err != nil {
		return nil, err
	}
	return c.finish(o, o.pos[0], func() (*edited, error) {
		w, err := c.workspace(o.pos[0])
		if err != nil {
			return nil, err
		}
		n, err := w.Resolve(o.pos[1])
		if err != nil {
			return nil, err
		}
		if err := checkExpect(o.expect, n, w); err != nil {
			return nil, err
		}
		out, err := n.rmEdit()
		if err != nil {
			return nil, err
		}
		return &edited{n.Doc, out, n.Parent.Segs()}, nil
	})
}
