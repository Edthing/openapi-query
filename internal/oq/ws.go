package oq

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	Base  string
	Root  *Doc
	docs  map[string]*Doc
	errs  map[string]error
	ops   []Op
	opsOK bool
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
