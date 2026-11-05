package oq

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// versionOverride is set at release time with -ldflags.
var versionOverride string

const baseVersion = "0.1.0"

func version() string {
	if versionOverride != "" {
		return versionOverride
	}
	return baseVersion
}

const defaultCap = 5000

type opts struct {
	pos        []string
	format     string // "" (tsv), "json", "yaml", "name" or "columns"
	columns    []string
	tsv        bool // -o tsv given explicitly
	metadata   bool
	maxTokens  int
	depth      int
	depthSet   bool
	expect     string
	inPlace    bool
	after      string
	afterSet   bool
	unresolved bool
	file       string
	header     bool
	help       bool
}

type result struct {
	data []byte
	hint string // narrower command shown when the cap is hit
}

type cmdFn func(c *cli, o *opts) (*result, error)

type cmdSpec struct {
	fn    cmdFn
	flags string // command-specific flags, space separated
	read  bool   // read commands honour the size cap and may take the spec from stdin
	usage string
	min   int // positional arguments including the spec (read commands)
	max   int
}

type cli struct {
	in       io.Reader
	out, err io.Writer
	stdinBuf []byte
	stdinGot bool
}

var commands map[string]cmdSpec

func init() {
	commands = map[string]cmdSpec{
		"outline": {fn: cmdOutline, flags: "--depth --header", read: true, min: 1, max: 2,
			usage: "oq outline <spec> [addr] [--depth N]"},
		"ls": {fn: cmdLs, flags: "--header", read: true, min: 2, max: 2,
			usage: "oq ls <spec> ops|schemas|tags|files"},
		"get": {fn: cmdGet, flags: "--metadata --header", read: true, min: 2, max: 2,
			usage: "oq get <spec> <addr> [--metadata]"},
		"at": {fn: cmdAt, read: true, min: 2, max: 2,
			usage: "oq at <spec> <line>[:<col>]"},
		"deps": {fn: cmdDeps, flags: "--depth --unresolved --header", read: true, min: 2, max: 2,
			usage: "oq deps <spec> <addr> [--depth N] [--unresolved]"},
		"refs": {fn: cmdRefs, flags: "--header", read: true, min: 2, max: 2,
			usage: "oq refs <spec> <addr>"},
		"set": {fn: cmdSet, flags: "--expect -i --file",
			usage: "oq set <spec> <addr> [--expect H] [-i] [--file F] < new.yaml"},
		"insert": {fn: cmdInsert, flags: "--after --expect -i --file",
			usage: "oq insert <spec> <addr> [--after KEY] [--expect H] [-i] [--file F] < new.yaml"},
		"rm": {fn: cmdRm, flags: "--expect -i",
			usage: "oq rm <spec> <addr> [--expect H] [-i]"},
	}
}

// Main runs oq and returns the process exit code.
func Main(args []string, in io.Reader, out, errw io.Writer) int {
	c := &cli{in: in, out: out, err: errw}
	code, err := c.run(args)
	if err != nil {
		fmt.Fprintf(errw, "oq: %s\n", err.Error())
	}
	return code
}

func (c *cli) run(args []string) (int, error) {
	if len(args) == 0 {
		fmt.Fprint(c.err, helpText)
		return ExitUsage, nil
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) == 1 || args[0] != "help" {
			fmt.Fprint(c.out, helpText)
			return 0, nil
		}
	case "version", "--version":
		fmt.Fprintln(c.out, "oq "+version())
		return 0, nil
	}
	if args[0] == "help" && len(args) > 1 {
		if sp, ok := commands[args[1]]; ok {
			fmt.Fprint(c.out, cmdHelp(args[1], sp))
			return 0, nil
		}
	}
	spec, ok := commands[args[0]]
	if !ok {
		return ExitUsage, fmt.Errorf("unknown command %q (try 'oq help')", args[0])
	}
	o, err := parseOpts(args[1:], spec)
	if err != nil {
		return ExitUsage, fmt.Errorf("%v\nusage: %s", err, spec.usage)
	}
	if o.help {
		fmt.Fprint(c.out, cmdHelp(args[0], spec))
		return 0, nil
	}
	if spec.read && spec.max > 0 && c.stdinPiped() && len(o.pos) < spec.max &&
		(len(o.pos) == spec.min-1 || (o.pos[0] != "-" && !fileExists(o.pos[0]))) {
		// no spec given and data is piped in: cat spec.yaml | oq outline
		o.pos = append([]string{"-"}, o.pos...)
	}
	res, err := spec.fn(c, o)
	if err != nil {
		if ee, ok := err.(*ExitError); ok {
			return ee.Code, ee
		}
		return ExitUsage, err
	}
	if res == nil {
		return 0, nil
	}
	if spec.read && o.maxTokens > 0 {
		if t := int(tokensOf(string(res.data))); t > o.maxTokens {
			msg := fmt.Sprintf("output is ~%d tokens, over the %d token cap; nothing printed", t, o.maxTokens)
			if res.hint != "" {
				msg += "\nnarrower: " + res.hint
			}
			msg += "\nor raise the cap with --max-tokens N (0 disables it)"
			return ExitCap, fmt.Errorf("%s", msg)
		}
	}
	c.out.Write(res.data)
	return 0, nil
}

func parseOpts(args []string, spec cmdSpec) (*opts, error) {
	o := &opts{maxTokens: defaultCap}
	allowed := map[string]bool{"-o": true, "--output": true, "--max-tokens": true, "-h": true, "--help": true}
	for _, f := range strings.Fields(spec.flags) {
		allowed[f] = true
	}
	allowed["--in-place"] = allowed["-i"]
	allowed["-f"] = allowed["--file"]
	endFlags := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if endFlags || a == "-" || !strings.HasPrefix(a, "-") {
			o.pos = append(o.pos, a)
			continue
		}
		if a == "--" {
			endFlags = true
			continue
		}
		name, val, hasVal := a, "", false
		if len(a) > 2 && a[0] == '-' && a[1] == 'o' { // -ojson
			name, val, hasVal = "-o", a[2:], true
		}
		if j := strings.IndexByte(a, '='); j > 0 && strings.HasPrefix(a, "--") {
			name, val, hasVal = a[:j], a[j+1:], true
		}
		if name == "--json" {
			return nil, fmt.Errorf("there is no --json flag; use -o json (or -o yaml; the default text output is cheapest)")
		}
		if !allowed[name] {
			return nil, fmt.Errorf("unknown or unsupported flag %s", name)
		}
		need := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s needs a value", name)
			}
			i++
			return args[i], nil
		}
		var err error
		var v string
		switch name {
		case "-o", "--output":
			if v, err = need(); err == nil {
				switch lv := strings.ToLower(v); {
				case lv == "tsv" || lv == "text" || lv == "raw":
					o.format = ""
					o.tsv = lv == "tsv"
				case lv == "json" || lv == "yaml" || lv == "name":
					o.format = lv
				case strings.HasPrefix(lv, "columns=") && len(lv) > 8:
					o.format = "columns"
					o.columns = strings.Split(v[8:], ",")
				default:
					err = fmt.Errorf("want tsv, json, yaml, name or columns=a,b")
				}
			}
		case "-h", "--help":
			o.help = true
		case "--metadata":
			o.metadata = true
		case "--header":
			o.header = true
		case "-i", "--in-place":
			o.inPlace = true
		case "--unresolved":
			o.unresolved = true
		case "--max-tokens":
			if v, err = need(); err == nil {
				o.maxTokens, err = strconv.Atoi(v)
				if err == nil && o.maxTokens < 0 {
					err = fmt.Errorf("must be >= 0")
				}
			}
		case "--depth":
			if v, err = need(); err == nil {
				o.depth, err = strconv.Atoi(v)
				o.depthSet = true
				if err == nil && o.depth < 1 {
					err = fmt.Errorf("must be >= 1")
				}
			}
		case "--expect":
			o.expect, err = need()
		case "--after":
			o.after, err = need()
			o.afterSet = true
		case "--file", "-f":
			o.file, err = need()
		}
		if err != nil {
			return nil, fmt.Errorf("bad value for %s: %v", name, err)
		}
	}
	return o, nil
}

// stdinPiped reports whether stdin is a pipe or file rather than a terminal.
func (c *cli) stdinPiped() bool {
	f, ok := c.in.(*os.File)
	if !ok {
		return true
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice == 0
}

func (c *cli) stdin() ([]byte, error) {
	if !c.stdinGot {
		c.stdinGot = true
		b, err := io.ReadAll(c.in)
		if err != nil {
			return nil, err
		}
		c.stdinBuf = b
	}
	return c.stdinBuf, nil
}

func (c *cli) workspace(spec string) (*WS, error) {
	var in []byte
	if spec == "-" {
		var err error
		if in, err = c.stdin(); err != nil {
			return nil, fail(ExitUsage, "reading stdin: %v", err)
		}
	}
	return NewWS(spec, in)
}

func need(o *opts, min, max int, usage string) error {
	if len(o.pos) < min || len(o.pos) > max {
		return fail(ExitUsage, "wrong number of arguments\nusage: %s", usage)
	}
	return nil
}

func shq(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r == '/' || r == '.' || r == '_' || r == '-' || r == '#' || r == ':' || r == '~' || r == '%' || r == '=' ||
			r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
