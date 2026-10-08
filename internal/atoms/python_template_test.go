package atoms

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// matrixTOML declares two cases: a star that must render a pyproject and no
// workflow tree, and a library with one answer fewer.
const matrixTOML = `parse = ["**/*.toml", "**/*.json"]

[[case]]
name = "star"
answers = { project_name = "Gate Py", mcp = true }
present = ["pyproject.toml", "src"]
absent = [".forgejo"]

[[case]]
name = "lib"
answers = { mcp = false }
present = ["pyproject.toml"]
`

// templateFiles is a template repository: a matrix, and a .git that is a
// directory.
func templateFiles(matrix string) map[string]string {
	return map[string]string{"ci-matrix.toml": matrix, ".git/HEAD": "ref: refs/heads/main\n", "template/x.jinja": "x"}
}

// renderer is a copier that writes the files it is told for the case named by
// the destination's last path element, and records what it was asked.
type renderer struct {
	t      *testing.T
	render map[string]map[string]string
	calls  []Cmd
	code   int
	out    string
	// skip is a copier that exits 0 and writes no destination at all.
	skip bool
}

func (r *renderer) answer(c Cmd) (string, int) {
	switch {
	case c.Name == "copier" && len(c.Args) == 1 && c.Args[0] == "--version":
		return "copier 9.18.2", 0
	case c.Name == "copier":
		r.calls = append(r.calls, c)
		dest := c.Args[len(c.Args)-1]
		if !r.skip {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				r.t.Fatal(err)
			}
		}
		for rel, body := range r.render[filepath.Base(dest)] {
			put(r.t, dest, rel, body)
		}
		return r.out, r.code
	}
	return "", 0
}

func okRender() map[string]map[string]string {
	return map[string]map[string]string{
		"star": {"pyproject.toml": "[project]\nname = \"x\"\n", "src/x.py": "x = 1\n", "data.json": "{}"},
		"lib":  {"pyproject.toml": "[project]\nname = \"y\"\n"},
	}
}

func TestTemplateRenderMatrix(t *testing.T) {
	const id = "template:render-matrix"
	t.Run("every case is rendered by copier into a directory of its own and graded", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)
		r := &renderer{t: t, render: okRender()}
		in, f := toolTree(t, templateFiles(matrixTOML), r.answer)
		before := listTree(t, in.Root)
		expect(t, runAtom(t, id, in), stateOf(0), pass, "render matrix: ci-matrix.toml  (2 case(s), copier "+checks.CopierVersion+")", "OK — star", "OK — lib")
		if len(r.calls) != 2 {
			t.Fatalf("%d renders, want 2", len(r.calls))
		}
		if probe := f.calls[0]; probe.Name != "copier" || flagged(probe) != "--version" {
			t.Errorf("the provisioning probe comes first: %+v", probe)
		}
		star := r.calls[0]
		// The argv is the chain's, from `copy` on: only the launcher is not.
		wantArgv, _ := checks.CopierArgv(in.Root, filepath.Join(filepath.Dir(star.Args[len(star.Args)-1]), "star"), map[string]any{"project_name": "Gate Py", "mcp": true})
		if star.Name != "copier" || !reflect.DeepEqual(star.Args, wantArgv[4:]) {
			t.Errorf("copier ran as %s %v, want %v", star.Name, star.Args, wantArgv[3:])
		}
		for _, want := range []string{"copy", "--trust", "--skip-tasks", "--vcs-ref=HEAD", "--defaults", "--quiet", "mcp=true", "project_name=Gate Py", in.Root} {
			if !slices.Contains(star.Args, want) {
				t.Errorf("copier was not given %q: %v", want, star.Args)
			}
		}
		if !star.Both || star.Dir != in.Root || !slices.Contains(star.Env, "PYTHONDONTWRITEBYTECODE=1") {
			t.Errorf("copier ran %+v", star)
		}
		// Every render went under the temp directory, never into the tree, and was removed.
		for _, c := range r.calls {
			dest := c.Args[len(c.Args)-1]
			if !strings.HasPrefix(dest, tmp) || strings.HasPrefix(dest, in.Root) {
				t.Errorf("copier rendered into %s, not under %s", dest, tmp)
			}
		}
		if left, _ := os.ReadDir(tmp); len(left) != 0 {
			t.Errorf("the renders were left behind: %v", left)
		}
		if after := listTree(t, in.Root); !reflect.DeepEqual(after, before) {
			t.Errorf("the template tree changed: before %v, after %v", before, after)
		}
	})
	t.Run("copier's program is where CopierArgv puts it", func(t *testing.T) {
		argv, err := checks.CopierArgv("/t", "/d", map[string]any{"a": "b"})
		if err != nil || argv[copierProgram] != "copier" || argv[copierProgram+1] != "copy" || argv[copierProgram-1] != "copier=="+checks.CopierVersion {
			t.Errorf("argv %v, %v: the launcher is %v", argv, err, argv[:copierProgram])
		}
	})
	for _, tc := range []struct {
		name     string
		rendered map[string]string
		says     string
	}{
		{"an empty tree", map[string]string{}, "rendered nothing — copier reported success but the tree is empty"},
		{"a conditional path that did not resolve", map[string]string{"pyproject.toml": "", "src/x.py": "", "{% if x %}only{% endif %}/a.txt": ""}, "unresolved jinja in rendered PATH"},
		{"a suffix copier did not strip", map[string]string{"pyproject.toml": "", "src/x.py": "", "a.py.jinja": ""}, "unstripped .jinja suffix: a.py.jinja"},
		{"a present expectation that is missing", map[string]string{"pyproject.toml": ""}, "expected PRESENT but missing: src"},
		{"an absent expectation that rendered, a directory by its name", map[string]string{"pyproject.toml": "", "src/x.py": "", ".forgejo/workflows/ci.yml": ""}, "expected ABSENT but rendered: .forgejo -> .forgejo"},
		{"a born-red stamp", map[string]string{"pyproject.toml": "", "src/x.py": "", "data.json": "{oops}"}, "data.json does not parse as json"},
		{"a toml that does not parse", map[string]string{"pyproject.toml": "[project\n", "src/x.py": ""}, "pyproject.toml does not parse as toml"},
		{"a suppression in the pour surface", map[string]string{"pyproject.toml": "", "src/x.py": "x = 1  # no" + "qa: E501\n"}, "a suppression in the POUR SURFACE reaches every repo born from this template"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &renderer{t: t, render: map[string]map[string]string{"star": tc.rendered, "lib": {"pyproject.toml": ""}}}
			in, _ := toolTree(t, templateFiles(matrixTOML), r.answer)
			expect(t, runAtom(t, id, in), stateOf(1), findings, tc.says, "::error::render matrix FAILED for: star")
		})
	}
	t.Run("a parse glob that matches a file nothing can parse is a problem, not a skip", func(t *testing.T) {
		matrix := strings.Replace(matrixTOML, `parse = ["**/*.toml", "**/*.json"]`, `parse = ["**/*.yaml"]`, 1)
		r := &renderer{t: t, render: map[string]map[string]string{"star": {"pyproject.toml": "", "src/x.py": "", "a.yaml": "a: 1"}, "lib": {"pyproject.toml": ""}}}
		in, _ := toolTree(t, templateFiles(matrix), r.answer)
		expect(t, runAtom(t, id, in), stateOf(1), findings, "no parser for `parse` match a.yaml")
	})
	t.Run("an answer that is not a scalar is the case's problem and copier is not run for it", func(t *testing.T) {
		matrix := "[[case]]\nname = \"bad\"\nanswers = { x = [1, 2] }\n"
		r := &renderer{t: t, render: okRender()}
		in, _ := toolTree(t, templateFiles(matrix), r.answer)
		expect(t, runAtom(t, id, in), stateOf(1), findings, "answer values must be scalars", "render matrix FAILED for: bad")
		if len(r.calls) != 0 {
			t.Errorf("copier ran for an argv that could not be built: %v", r.calls)
		}
	})
	t.Run("a copier that refused is the case's failure, with its last words", func(t *testing.T) {
		r := &renderer{t: t, render: okRender(), code: 1, out: "Error: conflict\nTemplate does not declare `variant`"}
		in, _ := toolTree(t, templateFiles(matrixTOML), r.answer)
		expect(t, runAtom(t, id, in), stateOf(1), findings, "copier render failed:", "Template does not declare `variant`", "render matrix FAILED for: star, lib")
	})
	t.Run("a copier that would not start never ran", func(t *testing.T) {
		r := &renderer{t: t, render: okRender(), code: -1, out: "copier: gone"}
		in, _ := toolTree(t, templateFiles(matrixTOML), r.answer)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the atom never ran: copier: gone")
	})
	t.Run("a render that reported success and wrote no directory cannot be read", func(t *testing.T) {
		r := &renderer{t: t, skip: true}
		in, _ := toolTree(t, templateFiles(matrixTOML), r.answer)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the atom never ran")
	})
	for _, target := range []string{"pyproject.toml", "src/x.py"} {
		t.Run("a rendered "+target+" that will not read cannot be graded", func(t *testing.T) {
			r := &renderer{t: t, render: okRender()}
			in, _ := toolTree(t, templateFiles(matrixTOML), func(c Cmd) (string, int) {
				out, code := r.answer(c)
				if dest := c.Args[len(c.Args)-1]; c.Name == "copier" && filepath.Base(dest) == "star" {
					// A link to nowhere: listed by the walk, unreadable by the grader.
					if err := os.Remove(filepath.Join(dest, target)); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(filepath.Join(dest, "nowhere"), filepath.Join(dest, target)); err != nil {
						t.Fatal(err)
					}
				}
				return out, code
			})
			expect(t, runAtom(t, id, in), stateOf(2), cannot, "the atom never ran")
		})
	}
	t.Run("a copier that fails its probe is unprovisioned", func(t *testing.T) {
		in, f := toolTree(t, templateFiles(matrixTOML), func(Cmd) (string, int) { return "gone", 127 })
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the phase's tool could not be provisioned", "copier --version exited 127")
		if len(f.calls) != 1 {
			t.Errorf("a render ran: %v", f.ran())
		}
	})

	t.Run("no ci-matrix.toml is absent and nothing runs", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"a.txt": "x"}, nil)
		expect(t, runAtom(t, id, in), stateOf(0), absent, "no ci-matrix.toml at the repository root")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
	t.Run("a tree that is not a repository cannot be rendered at its head", func(t *testing.T) {
		files := templateFiles(matrixTOML)
		delete(files, ".git/HEAD")
		in, f := toolTree(t, files, nil)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "no .git in the tree under check", "--vcs-ref=HEAD is load-bearing")
		if len(f.calls) != 0 {
			t.Errorf("copier ran without a repository: %v", f.ran())
		}
	})
	t.Run("a linked worktree's .git is a file, refused on purpose", func(t *testing.T) {
		files := templateFiles(matrixTOML)
		delete(files, ".git/HEAD")
		files[".git"] = "gitdir: /elsewhere/.git/worktrees/x\n"
		in, _ := toolTree(t, files, nil)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "no .git in the tree under check", "Here .git is a FILE, not a directory", "an index can be rebuilt, a history cannot")
	})
	t.Run("a matrix that does not parse, or declares no case, is a usage error", func(t *testing.T) {
		for name, matrix := range map[string]string{"garbage": "[[case", "no case": "parse = []\n", "a nameless case": "[[case]]\nanswers = {}\n"} {
			in, _ := toolTree(t, templateFiles(matrix), nil)
			if v := runAtom(t, id, in); v.State != 2 || !strings.Contains(v.Reason, "::error::") {
				t.Errorf("%s: state %d, %q", name, v.State, v.Reason)
			}
		}
	})
	t.Run("a matrix that would not read", func(t *testing.T) {
		in, _ := toolTree(t, templateFiles(matrixTOML), nil)
		if err := os.Remove(filepath.Join(in.Root, "ci-matrix.toml")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("nowhere", filepath.Join(in.Root, "ci-matrix.toml")); err != nil {
			t.Fatal(err)
		}
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "ci-matrix.toml could not be read")
	})
	t.Run("a root that cannot be read", func(t *testing.T) {
		expect(t, runAtom(t, id, missingRoot(t)), stateOf(2), cannot, "the repository root could not be read")
	})
}

func TestGlobMatches(t *testing.T) {
	all := []string{".forgejo/", ".forgejo/workflows/", ".forgejo/workflows/ci.yml", "a.toml", "src/", "src/b.toml", "src/deep/", "src/deep/c.toml", "src/x.py", "x.toml.bak"}
	for _, tc := range []struct {
		pattern string
		want    []string
	}{
		{"a.toml", []string{"a.toml"}},
		{"**/*.toml", []string{"a.toml", "src/b.toml", "src/deep/c.toml"}},
		{"src/*.toml", []string{"src/b.toml"}},
		{"src/**", []string{"src/", "src/b.toml", "src/deep/", "src/deep/c.toml", "src/x.py"}},
		{".forgejo", []string{".forgejo/"}},
		{"src/?.py", []string{"src/x.py"}},
		{"src/??.py", nil},
		{"a.toml.bak", nil},
		{"a.t?ml", []string{"a.toml"}},
		{"nothing", nil},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			if got := globMatches(tc.pattern, all); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%q matched %v, want %v", tc.pattern, got, tc.want)
			}
		})
	}
	if got := trimDirs([]string{".forgejo/", "a.toml"}); !reflect.DeepEqual(got, []string{".forgejo", "a.toml"}) {
		t.Errorf("trimDirs %v", got)
	}
}

func TestRenderedPaths(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "a.txt", "a")
	put(t, dir, "d/b.txt", "b")
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	files, all, err := renderedPaths(dir)
	if err != nil || !reflect.DeepEqual(files, []string{"a.txt", "d/b.txt"}) || !reflect.DeepEqual(all, []string{"a.txt", "d/", "d/b.txt", "empty/"}) {
		t.Errorf("files %v, all %v, %v", files, all, err)
	}
	if _, _, err := renderedPaths(filepath.Join(dir, "nope")); err == nil {
		t.Error("a destination that is not there walked")
	}
}

func TestTailLines(t *testing.T) {
	for _, tc := range []struct {
		out  string
		n    int
		want []string
	}{
		{"a\nb\nc\nd", 2, []string{"c", "d"}},
		{"a\nb", 5, []string{"a", "b"}},
		{"a\nb\nc\n\n", 3, []string{"a", "b", "c"}},
		{"", 3, []string{""}},
	} {
		if got := tailLines(tc.out, tc.n); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("tailLines(%q, %d) = %q, want %q", tc.out, tc.n, got, tc.want)
		}
	}
}
