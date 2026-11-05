package oq

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// ---- command plumbing ----

// edited is the result of applying one edit to one file.
type edited struct {
	doc  *Doc     // the file that was edited, as read
	out  []byte   // its new contents
	segs []string // node whose hash is reported with -o
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

// writeAtomic replaces path with out via a temp file in the same directory,
// but only if the file still holds old. It reports false when it does not.
func writeAtomic(path string, out, old []byte) (bool, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".oq-*")
	if err != nil {
		return false, err
	}
	done := false
	defer func() {
		if !done {
			os.Remove(tmp.Name())
		}
	}()
	if st, err := os.Stat(path); err == nil {
		tmp.Chmod(st.Mode().Perm())
	}
	_, err = tmp.Write(out)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, err
	}
	if cur, _ := os.ReadFile(path); !bytes.Equal(cur, old) {
		return false, nil
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return false, err
	}
	done = true
	return true, nil
}

// finish runs apply and writes the result to stdout, or to the file with -i.
// Under -i it holds a directory lock and re-applies the edit if the file
// changed between reading and renaming.
func (c *cli) finish(o *opts, apply func() (*edited, error)) (*result, error) {
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
	defer lockDir(filepath.Dir(path))()
	for try := 0; try < 5; try++ {
		if try > 0 {
			if e, err = apply(); err != nil {
				return nil, err
			}
		}
		ok, err := writeAtomic(path, e.out, e.doc.Src)
		if err != nil {
			return nil, fail(ExitUsage, "%v", err)
		}
		if ok {
			return writeRecord(o, e, path)
		}
	}
	return nil, fail(ExitStale, "stale: %s kept changing while writing", filepath.Base(path))
}

// writeRecord is the -o result of an in-place write: address, file, new hash.
func writeRecord(o *opts, e *edited, path string) (*result, error) {
	if o.format == "" {
		return nil, nil
	}
	nd, err := ParseDoc("", "", e.out)
	if err != nil {
		return nil, fail(ExitUsage, "%v", err)
	}
	h := ""
	if m := nd.Root.Descend(e.segs); m != nil {
		h = hashOf(m.DisplayText())
	}
	data, err := render([]Rec{{{"address", ptrString(e.segs)}, {"file", filepath.Base(path)}, {"hash", h}}}, o, true)
	if err != nil {
		return nil, err
	}
	return &result{data: data}, nil
}

// editOp applies one edit to the resolved node and names the node whose
// hash to report afterwards.
type editOp func(n *Node) (out []byte, report []string, err error)

// editAt resolves <spec> <addr>, checks --expect and applies op.
func (c *cli) editAt(o *opts, op editOp) (*result, error) {
	return c.finish(o, func() (*edited, error) {
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
		out, report, err := op(n)
		if err != nil {
			return nil, err
		}
		return &edited{doc: n.Doc, out: out, segs: report}, nil
	})
}

func cmdSet(c *cli, o *opts) (*result, error) {
	if err := need(o, 2, 2, commands["set"].usage); err != nil {
		return nil, err
	}
	t, err := c.readNew(o, o.pos[0])
	if err != nil {
		return nil, err
	}
	return c.editAt(o, func(n *Node) ([]byte, []string, error) {
		out, err := n.setEdit(t)
		return out, n.Segs(), err
	})
}

func cmdInsert(c *cli, o *opts) (*result, error) {
	if err := need(o, 2, 2, commands["insert"].usage); err != nil {
		return nil, err
	}
	t, err := c.readNew(o, o.pos[0])
	if err != nil {
		return nil, err
	}
	return c.editAt(o, func(n *Node) ([]byte, []string, error) {
		out, err := n.insertEdit(t, o.after, o.afterSet)
		return out, n.Segs(), err
	})
}

func cmdRm(c *cli, o *opts) (*result, error) {
	if err := need(o, 2, 2, commands["rm"].usage); err != nil {
		return nil, err
	}
	return c.editAt(o, func(n *Node) ([]byte, []string, error) {
		out, err := n.rmEdit()
		if err != nil {
			return nil, nil, err
		}
		return out, n.Parent.Segs(), nil
	})
}
