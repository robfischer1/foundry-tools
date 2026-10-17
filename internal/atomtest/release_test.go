package atomtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStarNamesTheStarAndOverlaysTheExtras(t *testing.T) {
	files := Star("hades", "FROM x\n", map[string]string{"go.mod": "module x\n", "Dockerfile": "FROM y\n"})
	if files[".copier-answers.yml"] != "service_name: hades\n" || files["go.mod"] != "module x\n" || files["Dockerfile"] != "FROM y\n" {
		t.Errorf("%v", files)
	}
	for _, b := range []string{BunBase, PythonBase, GoBase, RustBase} {
		if !strings.HasPrefix(b, "registry.notusmi.com/foundry/base-images/") || !strings.Contains(b, "@sha256:") {
			t.Errorf("%s is not a fleet base pinned by digest", b)
		}
	}
}

func TestWithDiesLaysTheRecordsAndNamesTheClone(t *testing.T) {
	in := WithDies(t, TreeIn(t, map[string]string{"a": "x"}), "http://o:8215/a.git", map[string]string{"a": `{"repo":"rob/a"}`})
	if in.Origin != "http://o:8215/a.git" {
		t.Errorf("origin %q", in.Origin)
	}
	b, err := os.ReadFile(filepath.Join(in.Dies, "fleet", "repos", "a.json"))
	if err != nil || string(b) != `{"repo":"rob/a"}` {
		t.Errorf("%q %v", b, err)
	}
}
