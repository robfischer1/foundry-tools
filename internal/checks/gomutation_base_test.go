package checks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		{"no common prefix means gomutants names files from the module root",
			"x/y\t/src/sub/x/y\nz\t/src/z\n", baseRoot, ""},
		{"a Windows line ending is not part of the directory",
			mod + "/cmd/fleet-door\t/src/cmd/fleet-door\r\n", baseRoot, "cmd/fleet-door"},
		{"a root with a trailing slash is the same root",
			mod + "/cmd/fleet-door\t/src/cmd/fleet-door\n", "/src/", "cmd/fleet-door"},
		{"packages with nothing in common name no base", "a/x\t/src/x\nb/y\t/src/y\n", baseRoot, ""},
		{"packages whose first segments differ name no base", "a\t/src/a\nb\t/src/b\n", baseRoot, ""},
		{"a line with no import path is skipped",
			"\t/src/zzz\n" + mod + "/cmd/fleet-door\t/src/cmd/fleet-door\n", baseRoot, "cmd/fleet-door"},
		{"a line with no directory is skipped",
			mod + "/cmd/zzz\t\n" + mod + "/cmd/fleet-door\t/src/cmd/fleet-door\n", baseRoot, "cmd/fleet-door"},
		{"a line with no tab is skipped",
			"garbage\n" + mod + "/cmd/fleet-door\t/src/cmd/fleet-door\n", baseRoot, "cmd/fleet-door"},
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
	// Keys out of sorted order: a re-marshal would reorder them, so equal bytes
	// mean the report was never touched.
	in := []byte(`{"go_module":"m","files":[{"mutations":[],"file_name":"cmd/x/a.go"}],"elapsed_time":1}`)
	out, err := RebaseGoReport(in, "")
	if err != nil || string(out) != string(in) {
		t.Fatalf("got %s, %v", out, err)
	}
}

func TestRebaseGoReportRefusesWhatItCannotRead(t *testing.T) {
	for name, c := range map[string]struct{ in, want string }{
		"not json":       {`nope`, "is not a gomutants report"},
		"no files":       {`{"go_module":"m"}`, "no readable files"},
		"files not list": {`{"files":5}`, "no readable files"},
		"no file_name":   {`{"files":[{"mutations":[]}]}`, "no readable file_name"},
	} {
		if _, err := RebaseGoReport([]byte(c.in), "cmd"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, c.want)
		}
	}
}
