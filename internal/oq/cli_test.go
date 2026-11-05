package oq

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func run(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main(args, strings.NewReader(stdin), &out, &errb)
	return out.String(), errb.String(), code
}

func td(name string) string { return filepath.Join("..", "..", "testdata", name) }

func TestReadCommands(t *testing.T) {
	pet := td("petstore.yaml")
	cases := []struct {
		name  string
		args  []string
		want  string // substring of stdout
		exact string // exact stdout, if set
		code  int
	}{
		{"outline root", []string{"outline", pet}, "/paths\tobject\t", "", 0},
		{"ls ops", []string{"ls", pet, "ops"}, "/paths/~1pets~1{pet_id}/get\tGET\t/pets/{pet_id}\tgetPet\t", "", 0},
		{"ls schemas", []string{"ls", pet, "schemas"}, "/components/schemas/odd%20key~1with%20space\todd key/with space\t", "", 0},
		{"ls tags", []string{"ls", pet, "tags"}, "/tags/0\tpets\t", "", 0},
		{"get scalar", []string{"get", pet, "/info/title"}, "", "Pets\n", 0},
		{"outline path item", []string{"outline", pet, "path:/pets"}, "/paths/~1pets/post\tobject\t", "", 0},
		{"missing path", []string{"get", pet, "path:/nope"}, "", "", ExitNotFound},
		{"get opid", []string{"get", pet, "opid:getPet"}, "operationId: getPet\n", "", 0},
		{"get method path", []string{"get", pet, "GET /pets/{pet_id}"}, "default:\n", "", 0},
		{"get schema dedented", []string{"get", pet, "schema:Error"}, "", "type: object\nproperties:\n  message:\n    type: string\n", 0},
		{"get hash pointer form", []string{"get", pet, "#/info/version"}, "", "\"1.0\"\n", 0},
		{"at line", []string{"at", pet, "69"}, "", "/components/schemas/Pet/properties/id/type\n", 0},
		{"at key line 68", []string{"at", pet, "68"}, "", "/components/schemas/Pet/properties/id\n", 0},
		{"at key line", []string{"at", pet, "64"}, "", "/components/schemas/Pet\n", 0},
		{"at line:col", []string{"at", pet, "69:9"}, "", "/components/schemas/Pet/properties/id\n", 0},
		{"deps", []string{"deps", pet, "opid:getPet", "--depth", "2"}, "/components/schemas/Owner\t", "", 0},
		{"deps unresolved", []string{"deps", pet, "/", "--unresolved"}, "/components/schemas/Nope\t-\t1\t/components/schemas/Missing\tpointer not found\n", "", 0},
		{"refs", []string{"refs", pet, "schema:Error"}, "\t/components/schemas/Error\t/paths/~1pets~1{pet_id}/get\n", "", 0},
		{"missing address", []string{"get", pet, "/nope"}, "", "", ExitNotFound},
		{"missing opid", []string{"get", pet, "opid:zzz"}, "", "", ExitNotFound},
		{"line past end", []string{"at", pet, "9999"}, "", "", ExitNotFound},
		{"bad usage", []string{"get", pet}, "", "", ExitUsage},
		{"unknown flag", []string{"get", pet, "/info", "--bogus"}, "", "", ExitUsage},
		{"missing file", []string{"get", "nope.yaml", "/"}, "", "", ExitUsage},
		{"cap", []string{"get", pet, "/paths", "--max-tokens", "10"}, "", "", ExitCap},
		{"cap off", []string{"get", pet, "/paths", "--max-tokens", "0"}, "operationId: listPets", "", 0},
		{"json mini", []string{"ls", td("mini.json"), "ops", "-o", "json"}, `"operationId":"getA"`, "", 0},
		{"multi ext get", []string{"get", td("multi/root.yaml"), "schemas/item.yaml#/Tag"}, "", "type: string\n", 0},
		{"multi deps", []string{"deps", td("multi/root.yaml"), "opid:getItem"}, "schemas/item.yaml#/Item\t", "", 0},
		{"multi files", []string{"ls", td("multi/root.yaml"), "files"}, "schemas/missing.yaml\t-\tmissing", "", 0},
	}
	for _, c := range cases {
		out, errs, code := run(t, "", c.args...)
		if code != c.code {
			t.Errorf("%s: exit %d want %d (stderr %q)", c.name, code, c.code, errs)
			continue
		}
		if c.code != 0 && out != "" {
			t.Errorf("%s: stdout must be empty on failure, got %q", c.name, out)
		}
		if c.exact != "" && out != c.exact {
			t.Errorf("%s: got %q want %q", c.name, out, c.exact)
		}
		if c.want != "" && !strings.Contains(out, c.want) {
			t.Errorf("%s: %q not in %q", c.name, c.want, out)
		}
	}
}

// Every address printed by outline must be accepted by get.
func TestAddressRoundTrip(t *testing.T) {
	for _, f := range []string{td("petstore.yaml"), td("mini.json"), td("multi/root.yaml")} {
		out, _, _ := run(t, "", "outline", f, "/", "--depth", "9", "--max-tokens", "0")
		n := 0
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			addr := strings.Split(l, "\t")[0]
			if _, errs, code := run(t, "", "get", f, addr, "--max-tokens", "0"); code != 0 {
				t.Errorf("%s: get %q failed: %s", f, addr, errs)
			}
			n++
		}
		if n == 0 {
			t.Errorf("%s: empty outline", f)
		}
	}
}

func TestStdinSpec(t *testing.T) {
	src, _ := os.ReadFile(td("petstore.yaml"))
	out, _, code := run(t, string(src), "get", "-", "/info/title")
	if code != 0 || out != "Pets\n" {
		t.Fatalf("got %q %d", out, code)
	}
}

func TestWrites(t *testing.T) {
	pet := td("petstore.yaml")
	orig, _ := os.ReadFile(pet)
	changedLines := func(out string) int {
		a, b := strings.Split(string(orig), "\n"), strings.Split(out, "\n")
		n := 0
		for _, l := range a {
			found := false
			for _, m := range b {
				if l == m {
					found = true
					break
				}
			}
			if !found {
				n++
			}
		}
		return n
	}
	t.Run("set unchanged is identical", func(t *testing.T) {
		for _, addr := range []string{"/info", "/info/description", "schema:Pet", "/paths/~1pets/get/tags", "/tags/1"} {
			txt, _, _ := run(t, "", "get", pet, addr, "--max-tokens", "0")
			out, errs, code := run(t, txt, "set", pet, addr)
			if code != 0 || out != string(orig) {
				t.Errorf("%s: code %d err %q identical=%v", addr, code, errs, out == string(orig))
			}
		}
	})
	t.Run("set scalar touches one line", func(t *testing.T) {
		out, _, code := run(t, "integer\n", "set", pet, "/components/schemas/Error/properties/message/type")
		if code != 0 || changedLines(out) != 1 || !strings.Contains(out, "          type: integer\n") {
			t.Fatalf("code %d changed %d", code, changedLines(out))
		}
	})
	t.Run("set block replaces block", func(t *testing.T) {
		out, _, code := run(t, "type: object\nproperties:\n  code:\n    type: integer\n", "set", pet, "schema:Error")
		if code != 0 || !strings.Contains(out, "        code:\n          type: integer\n") || strings.Contains(out, "message") {
			t.Fatalf("code %d\n%s", code, out)
		}
	})
	t.Run("insert after", func(t *testing.T) {
		out, _, code := run(t, "Tag:\n  type: string\n", "insert", pet, "/components/schemas", "--after", "Owner")
		if code != 0 || !strings.Contains(out, "            $ref: '#/components/schemas/Pet'\n    Tag:\n      type: string\n") {
			t.Fatalf("code %d\n%s", code, out)
		}
	})
	t.Run("insert duplicate key refused", func(t *testing.T) {
		_, _, code := run(t, "Pet:\n  type: string\n", "insert", pet, "/components/schemas")
		if code != ExitUsage {
			t.Fatalf("code %d", code)
		}
	})
	t.Run("rm", func(t *testing.T) {
		out, _, code := run(t, "", "rm", pet, "schema:Error")
		if code != 0 || strings.Contains(out, "Error:\n") {
			t.Fatalf("code %d", code)
		}
	})
	t.Run("rm root refused", func(t *testing.T) {
		if _, _, code := run(t, "", "rm", pet, "/"); code != ExitUsage {
			t.Fatalf("code %d", code)
		}
	})
	t.Run("expect mismatch", func(t *testing.T) {
		out, _, code := run(t, "x\n", "set", pet, "/info/title", "--expect", "000000000000")
		if code != ExitStale || out != "" {
			t.Fatalf("code %d", code)
		}
	})
	t.Run("json minified", func(t *testing.T) {
		out, _, code := run(t, "type: object\nproperties:\n  z: {type: integer}\n", "set", td("mini.json"), "schema:B")
		if code != 0 || !strings.Contains(out, `"B":{"type":"object","properties":{"z":{"type":"integer"}}}}}}`) {
			t.Fatalf("code %d\n%s", code, out)
		}
		out, _, code = run(t, "", "rm", td("mini.json"), "schema:A")
		if code != 0 || !strings.Contains(out, `"schemas":{"B":{"type":"string"}}`) {
			t.Fatalf("code %d\n%s", code, out)
		}
	})
}

func TestInPlaceAndExpect(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "s.yaml")
	src, _ := os.ReadFile(td("petstore.yaml"))
	os.WriteFile(f, src, 0o640)
	out, _, code := run(t, "", "get", f, "/info/title", "-o", "columns=hash")
	if code != 0 {
		t.Fatal(out)
	}
	h := strings.TrimSpace(out)
	if len(h) != 12 {
		t.Fatalf("bad hash %q", h)
	}
	if _, errs, code := run(t, "Hello\n", "set", f, "/info/title", "--expect", h, "-i"); code != 0 {
		t.Fatalf("code %d %s", code, errs)
	}
	got, _ := os.ReadFile(f)
	if !strings.Contains(string(got), "title: Hello # the title") {
		t.Fatalf("not written:\n%s", got)
	}
	if st, _ := os.Stat(f); st.Mode().Perm() != 0o640 {
		t.Errorf("mode changed: %v", st.Mode())
	}
	// the old hash is now stale
	if _, _, code := run(t, "Again\n", "set", f, "/info/title", "--expect", h, "-i"); code != ExitStale {
		t.Fatalf("want stale, got %d", code)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("temp files left behind: %v", ents)
	}
}

func TestConcurrentInPlace(t *testing.T) {
	f := filepath.Join(t.TempDir(), "s.yaml")
	src, _ := os.ReadFile(td("petstore.yaml"))
	os.WriteFile(f, src, 0o644)
	done := make(chan int, 12)
	for i := 0; i < 12; i++ {
		go func(i int) {
			_, _, code := run(t, "k"+itoa(i)+": v\n", "insert", f, "/info", "-i")
			done <- code
		}(i)
	}
	for i := 0; i < 12; i++ {
		if code := <-done; code != 0 {
			t.Errorf("writer exited %d", code)
		}
	}
	out, _, _ := run(t, "", "outline", f, "/info")
	if n := strings.Count(out, "/info/k"); n != 12 {
		t.Fatalf("expected 12 inserted keys, found %d:\n%s", n, out)
	}
}

func TestImplicitStdinSpec(t *testing.T) {
	src, _ := os.ReadFile(td("petstore.yaml"))
	for _, args := range [][]string{{"outline"}, {"outline", "/info"}, {"get", "/info/title"}, {"ls", "ops"}, {"at", "2"}} {
		if out, errs, code := run(t, string(src), args...); code != 0 || out == "" {
			t.Errorf("%v: code %d %q", args, code, errs)
		}
	}
	// an existing file still wins over stdin
	if out, _, _ := run(t, "not: [valid", "get", td("petstore.yaml"), "/info/title"); out != "Pets\n" {
		t.Errorf("got %q", out)
	}
}

func TestHelpAndHeader(t *testing.T) {
	pet := td("petstore.yaml")
	for _, args := range [][]string{{"outline", "--help"}, {"outline", pet, "/paths", "--depth", "2", "-h"}, {"help", "set"}, {"set", "--help"}} {
		out, _, code := run(t, "", args...)
		if code != 0 || !strings.HasPrefix(out, "usage: oq ") {
			t.Errorf("%v: code %d out %q", args, code, out)
		}
	}
	out, _, code := run(t, "", "outline", pet, "--header")
	if code != 0 || !strings.HasPrefix(out, "address\tkind\ttokens\tline\tendLine\tnote\n/openapi\t") {
		t.Errorf("header: %d %q", code, out)
	}
	out, _, _ = run(t, "", "ls", pet, "ops", "--header")
	if !strings.HasPrefix(out, "address\tmethod\tpath\toperationId\ttokens\n") {
		t.Errorf("ls header: %q", out)
	}
	if _, _, code := run(t, "", "get", pet, "/info", "--header"); code != ExitUsage {
		t.Errorf("get --header should be a usage error, got %d", code)
	}
}

func TestOutputNameAndColumns(t *testing.T) {
	pet := td("petstore.yaml")
	out, _, code := run(t, "", "ls", pet, "ops", "-o", "name")
	if code != 0 || out != "/paths/~1pets/get\n/paths/~1pets/post\n/paths/~1pets~1{pet_id}/get\n" {
		t.Errorf("name: %d %q", code, out)
	}
	out, _, code = run(t, "", "ls", pet, "ops", "-o", "columns=operationId,method", "--header")
	if code != 0 || !strings.HasPrefix(out, "operationId\tmethod\nlistPets\tGET\n") {
		t.Errorf("columns: %d %q", code, out)
	}
	if _, errs, code := run(t, "", "ls", pet, "ops", "-o", "columns=nope"); code != ExitUsage || !strings.Contains(errs, "have: address") {
		t.Errorf("bad column: %d %q", code, errs)
	}
	out, _, _ = run(t, "", "deps", pet, "opid:getPet", "-o", "name")
	if out != "/components/schemas/Pet\n/components/schemas/Error\n" {
		t.Errorf("deps name: %q", out)
	}
}

// Every format of every read command must parse back to the same data.
func TestFormatsRoundTrip(t *testing.T) {
	pet := td("petstore.yaml")
	cmds := [][]string{
		{"outline", pet, "/", "--depth", "3"},
		{"ls", pet, "ops"}, {"ls", pet, "schemas"}, {"ls", pet, "tags"},
		{"deps", pet, "opid:getPet", "--depth", "3"},
		{"refs", pet, "schema:Pet"},
		{"ls", td("multi/root.yaml"), "files"},
	}
	for _, c := range cmds {
		tsv, _, code := run(t, "", append(c, "--max-tokens", "0")...)
		if code != 0 {
			t.Fatalf("%v: %d", c, code)
		}
		rows := strings.Count(tsv, "\n")
		for _, f := range []string{"json", "yaml"} {
			out, errs, code := run(t, "", append(c, "-o", f, "--max-tokens", "0")...)
			if code != 0 {
				t.Fatalf("%v -o %s: %d %s", c, f, code, errs)
			}
			var recs []map[string]any
			var err error
			if f == "json" {
				err = json.Unmarshal([]byte(out), &recs)
			} else {
				err = yaml.Unmarshal([]byte(out), &recs)
			}
			if err != nil || len(recs) != rows {
				t.Errorf("%v -o %s: err=%v got %d records want %d\n%s", c, f, err, len(recs), rows, out)
			}
		}
	}
}

// get -o yaml/json print the node itself; --metadata prints only metadata.
func TestGetFormats(t *testing.T) {
	pet := td("petstore.yaml")
	for _, addr := range []string{"/info", "/info/description", "schema:Pet", "GET /pets/{pet_id}", "/info/title", "/paths/~1pets/get/tags"} {
		raw, _, _ := run(t, "", "get", pet, addr, "--max-tokens", "0")
		if y, _, _ := run(t, "", "get", pet, addr, "-o", "yaml"); y != raw {
			t.Errorf("%s: -o yaml should equal the raw text\ngot %q\nwant %q", addr, y, raw)
		}
		j, errs, code := run(t, "", "get", pet, addr, "-o", "json")
		if code != 0 {
			t.Fatalf("%s -o json: %d %s", addr, code, errs)
		}
		var fromJSON, fromYAML any
		if err := json.Unmarshal([]byte(j), &fromJSON); err != nil {
			t.Fatalf("%s: json does not parse: %v\n%s", addr, err, j)
		}
		if strings.Contains(j, `"hash"`) || strings.Contains(j, `"address"`) {
			t.Errorf("%s: json must be the node, not metadata:\n%s", addr, j)
		}
		if err := yaml.Unmarshal([]byte(strings.ReplaceAll(raw, "&pet", "")), &fromYAML); err != nil {
			t.Fatalf("%s: %v", addr, err)
		}
		a, _ := json.Marshal(fromJSON)
		b, _ := json.Marshal(fromYAML)
		if string(a) != string(b) {
			t.Errorf("%s: json differs from yaml value\n%s\n%s", addr, a, b)
		}
	}
	// metadata
	row, _, code := run(t, "", "get", pet, "/info/title", "--metadata")
	if code != 0 || strings.Count(row, "\t") != 10 || strings.Count(row, "\n") != 1 || !strings.HasPrefix(row, "/info/title\tstring\t") {
		t.Errorf("metadata row: %d %q", code, row)
	}
	hdr, _, _ := run(t, "", "get", pet, "/info/title", "--metadata", "--header")
	if !strings.HasPrefix(hdr, "address\tkind\tfile\tline\tcol\tendLine\tendCol\tstartByte\tendByte\thash\ttokens\n") {
		t.Errorf("metadata header: %q", hdr)
	}
	for _, f := range []string{"json", "yaml"} {
		out, errs, code := run(t, "", "get", pet, "/info", "--metadata", "-o", f)
		var rec map[string]any
		var err error
		if f == "json" {
			err = json.Unmarshal([]byte(out), &rec)
		} else {
			err = yaml.Unmarshal([]byte(out), &rec)
		}
		if code != 0 || err != nil || rec["address"] != "/info" || rec["hash"] == nil || rec["text"] != nil {
			t.Errorf("--metadata -o %s: %d %v %s\n%s", f, code, err, errs, out)
		}
	}
	if h, _, _ := run(t, "", "get", pet, "/info", "-o", "columns=hash"); len(strings.TrimSpace(h)) != 12 {
		t.Errorf("columns=hash: %q", h)
	}
	if a, _, _ := run(t, "", "get", pet, "/info", "-o", "name"); a != "/info\n" {
		t.Errorf("name: %q", a)
	}
	if _, errs, code := run(t, "", "get", pet, "/info", "-o", "tsv"); code != ExitUsage || !strings.Contains(errs, "--metadata") {
		t.Errorf("get -o tsv: %d %q", code, errs)
	}
}
