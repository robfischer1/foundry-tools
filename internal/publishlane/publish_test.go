package publishlane

import "testing"

func TestArtifactOfReadsWheelsAndSdists(t *testing.T) {
	cases := []struct {
		file          string
		ok            bool
		dist, version string
	}{
		{"stellar_core-1.43.0-py3-none-any.whl", true, "stellar_core", "1.43.0"},
		{"stellar_core-1.43.0.tar.gz", true, "stellar_core", "1.43.0"},
		{"stellar_core-1.44.0rc1.tar.gz", true, "stellar_core", "1.44.0rc1"},
		// A build tag sits between the version and the python tag.
		{"tethys-0.2.0-1-cp314-cp314-manylinux_2_39_x86_64.whl", true, "tethys", "0.2.0"},
		// uv writes a .gitignore beside what it built.
		{".gitignore", false, "", ""},
		{"stellar_core-1.43.0.zip", false, "", ""},
		{"stellar_core-1.43.0-py3-none.whl", false, "", ""},
		{"stellar-core-1.43.0.tar.gz", false, "", ""},
	}
	for _, c := range cases {
		a, ok := ArtifactOf(c.file)
		if ok != c.ok || a.Dist != c.dist || a.Version != c.version {
			t.Errorf("ArtifactOf(%q) = %+v, %v; want dist %q version %q, %v", c.file, a, ok, c.dist, c.version, c.ok)
		}
		if ok && a.File != c.file {
			t.Errorf("ArtifactOf(%q).File = %q; the index lists the file as written", c.file, a.File)
		}
	}
}

func TestIndexNameIsThePEP503Name(t *testing.T) {
	for dist, want := range map[string]string{
		"stellar_core":   "stellar-core",
		"mnemosyne_core": "mnemosyne-core",
		"Foo.Bar__baz":   "foo-bar-baz",
		"athena":         "athena",
	} {
		if got := IndexName(dist); got != want {
			t.Errorf("IndexName(%q) = %q, want %q", dist, got, want)
		}
	}
}

// The page Nexus answers for a hosted project: one anchor per file, the href
// carrying the digest as a fragment.
const page = `<html><body><h1>Links for stellar-core</h1>
<a href="../../packages/stellar-core/1.43.0/stellar_core-1.43.0-py3-none-any.whl#sha256=aa" rel="internal">stellar_core-1.43.0-py3-none-any.whl</a><br/>
<a href="../../packages/stellar-core/1.43.0/stellar_core-1.43.0.tar.gz#sha256=bb" rel="internal">stellar_core-1.43.0.tar.gz</a><br/>
</body></html>`

func TestReleasedReadsTheFileOffTheProjectPage(t *testing.T) {
	cases := []struct {
		file string
		want bool
	}{
		{"stellar_core-1.43.0.tar.gz", true},
		{"stellar_core-1.43.0-py3-none-any.whl", true},
		{"stellar_core-1.44.0.tar.gz", false},
		// A released file whose name ENDS with this one's is not this one.
		{"core-1.43.0.tar.gz", false},
		// Nor is one this name is a prefix of.
		{"stellar_core-1.43.0.tar", false},
	}
	for _, c := range cases {
		if got := Released(page, c.file); got != c.want {
			t.Errorf("Released(page, %q) = %v, want %v", c.file, got, c.want)
		}
	}
	if Released("", "stellar_core-1.43.0.tar.gz") {
		t.Error("an empty page releases nothing")
	}
}

func TestProbeSplitsThePageFromItsStatus(t *testing.T) {
	cases := []struct {
		out    string
		status int
		page   string
	}{
		{"<html>a</html>\n200", 200, "<html>a</html>"},
		{"line one\nline two\n200\n", 200, "line one\nline two"},
		{"404", 404, ""},
		{"\n404", 404, ""},
	}
	for _, c := range cases {
		status, got, err := Probe(c.out)
		if err != nil || status != c.status || got != c.page {
			t.Errorf("Probe(%q) = %d, %q, %v; want %d, %q", c.out, status, got, err, c.status, c.page)
		}
	}
	if _, _, err := Probe("<html>no status</html>"); err == nil {
		t.Error("an answer with no status line must be an error, not a page")
	}
}
