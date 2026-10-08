package checks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const baseRoot = "/src"

func TestGoReportBase(t *testing.T) {
	const mod = "git.notusmi.com/rob/paneless"
	cases := []struct {
		name, list, root, want string
	}{
		{"a lone package in cmd/<name> (the paneless layout)",
			mod + "/cmd/fleet-door\t/src/cmd/fleet-door\n", baseRoot, "cmd/fleet-door"},
		{"packages spanning the tree are module-relative",
			mod + "\t/src\n" + mod + "/cmd/fleet-door\t/src/cmd/fleet-door\n" + mod + "/internal/x\t/src/internal/x\n", baseRoot, ""},
		{"two cmd packages share cmd",
			mod + "/cmd/a\t/src/cmd/a\n" + mod + "/cmd/b\t/src/cmd/b\n", baseRoot, "cmd"},
		{"the order of the listing does not matter",
			mod + "/cmd/b\t/src/cmd/b\n" + mod + "/cmd/a\t/src/cmd/a\n", baseRoot, "cmd"},
		{"a module below the repo root",
			mod + "/cmd/x\t/src/tools/go/cmd/x\n", "/src/tools/go", "cmd/x"},
		{"a lone package at the module root", mod + "\t/src\n", baseRoot, ""},
		{"a string prefix that is not a whole segment is gomutants' own",
			mod + "/a/foo\t/src/a/foo\n" + mod + "/a/foobar\t/src/a/foobar\n", baseRoot, "a/foo"},
		{"nothing listed", "", baseRoot, ""},
		{"a line without a tab is not a package", "garbage\n", baseRoot, ""},
		{"a directory outside the root cannot be named", mod + "/x\t/elsewhere/x\n", baseRoot, ""},
		{"a dir that does not end in the import path's tail", mod + "/cmd/x\t/src/other\n" + mod + "/cmd/y\t/src/cmd/y\n", baseRoot, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := GoReportBase(c.list, c.root); got != c.want {
				t.Errorf("GoReportBase = %q, want %q", got, c.want)
			}
		})
	}
}

// The layout that failed: a module whose only package is cmd/<name>. The rebased
// names must be real paths under the module root, which is what mutation-gate
// checks first.
func TestRebaseGoReportNamesRealFilesFromTheModuleRoot(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"answer.go", "main.go"} {
		p := filepath.Join(root, "cmd", "fleet-door", f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	list := "git.notusmi.com/rob/paneless/cmd/fleet-door\t" + filepath.Join(root, "cmd", "fleet-door") + "\n"
	report := []byte(`{"go_module":"git.notusmi.com/rob/paneless","elapsed_time":2,"mutants_lived":1,"files":[` +
		`{"file_name":"answer.go","mutations":[{"type":"RETURN_ZERO","status":"LIVED","line":118,"column":9}]},` +
		`{"file_name":"main.go","mutations":[{"type":"BRANCH_IF","status":"KILLED","line":60,"column":17}]}]}`)

	base := GoReportBase(list, root)
	out, err := RebaseGoReport(report, base)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		GoModule string  `json:"go_module"`
		Elapsed  float64 `json:"elapsed_time"`
		Lived    int     `json:"mutants_lived"`
		Files    []struct {
			FileName  string `json:"file_name"`
			Mutations []struct {
				Type, Status string
				Line, Column int
			} `json:"mutations"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.GoModule != "git.notusmi.com/rob/paneless" || got.Elapsed != 2 || got.Lived != 1 {
		t.Errorf("the rest of the report changed: %s", out)
	}
	want := []string{"cmd/fleet-door/answer.go", "cmd/fleet-door/main.go"}
	if len(got.Files) != 2 {
		t.Fatalf("files: %s", out)
	}
	for i, f := range got.Files {
		if f.FileName != want[i] {
			t.Errorf("file %d = %q, want %q", i, f.FileName, want[i])
		}
		if _, err := os.Stat(filepath.Join(root, f.FileName)); err != nil {
			t.Errorf("%s is not under the module root: %v", f.FileName, err)
		}
		if len(f.Mutations) != 1 {
			t.Errorf("%s lost its mutations: %s", f.FileName, out)
		}
	}
	if got.Files[0].Mutations[0].Line != 118 || got.Files[0].Mutations[0].Status != "LIVED" {
		t.Errorf("a mutation changed: %s", out)
	}
}

func TestRebaseGoReportLeavesAModuleRelativeReportAlone(t *testing.T) {
	in := []byte(`{"files":[{"file_name":"cmd/x/a.go","mutations":[]}]}`)
	out, err := RebaseGoReport(in, "")
	if err != nil || string(out) != string(in) {
		t.Fatalf("got %s, %v", out, err)
	}
}

func TestRebaseGoReportRefusesWhatItCannotRead(t *testing.T) {
	for name, in := range map[string]string{
		"not json":       `nope`,
		"no files":       `{"go_module":"m"}`,
		"files not list": `{"files":5}`,
		"no file_name":   `{"files":[{"mutations":[]}]}`,
	} {
		if _, err := RebaseGoReport([]byte(in), "cmd"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
