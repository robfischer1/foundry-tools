package vulncheck

import (
	"context"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// rule answers an exec whose argv line contains the needle.
type rule struct {
	needle string
	out    string
	code   int
}

// fake is an Exec that records every run and answers from its rules, first
// match wins; a run no rule matches exits 0 saying nothing.
type fake struct {
	rules []rule
	runs  []string
}

func (f *fake) exec(_ context.Context, c atoms.Cmd) (string, int) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	f.runs = append(f.runs, c.Dir+": "+line)
	for _, r := range f.rules {
		if strings.Contains(line, r.needle) {
			return r.out, r.code
		}
	}
	return "", 0
}

func input(f *fake, files ...string) atomkit.In {
	return atomkit.In{Root: "/src", Files: files, Exec: f.exec, Now: time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)}
}

const allowedReport = "Vulnerability #1: GO-2026-6508\n    Fixed in: go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc@v0.21.0\n"

// row is go:govulncheck's, the row the tests below judge under; the dev atom's
// own is held by TestTheVerdictSaysTheRowThatAsked.
var row = checks.AtomDef{ID: "go:govulncheck", Stage: checks.StagePrepush, Lane: checks.LaneGo}

func check(ctx context.Context, in atomkit.In) checks.Verdict { return Check(ctx, in, row) }

// The verdict carries the row Check was handed: a second atom running the same
// scan reports under its own id and stage, for a pass and for a finding.
func TestTheVerdictSaysTheRowThatAsked(t *testing.T) {
	dev := checks.AtomDef{ID: "dev:go-vuln", Stage: "dev", Lane: checks.LaneGo}
	v := Check(t.Context(), input(&fake{}, "go.mod"), dev)
	if v.Atom != dev.ID || v.Stage != "dev" || v.State != 0 || v.Reason != "dev:go-vuln: PASS" {
		t.Errorf("clean: %+v", v)
	}
	f := &fake{rules: []rule{{"govulncheck ./...", "Vulnerability #1: GO-2026-0001\n", 3}}}
	v = Check(t.Context(), input(f, "go.mod"), dev)
	if v.Atom != dev.ID || v.State != 1 || !strings.Contains(v.Reason, "dev:go-vuln: FINDINGS (exit 1)") {
		t.Errorf("finding: %+v", v)
	}
}

// The modules are downloaded first, the tool proved, and then the scan runs, in
// the module's directory, each its own exec.
func TestTheScanRunsAfterTheDownloadAndTheVersionProbe(t *testing.T) {
	f := &fake{}
	v := check(t.Context(), input(f, "go.mod", "main.go"))
	if v.State != 0 || v.Reason != "go:govulncheck: PASS" {
		t.Fatalf("%+v", v)
	}
	want := []string{"/src: go mod download", "/src: govulncheck -version", "/src: govulncheck ./..."}
	if strings.Join(f.runs, "|") != strings.Join(want, "|") {
		t.Errorf("runs %v", f.runs)
	}
}

// govulncheck answers 3 for a finding, and an advisory the gate allows turns it
// into a pass that says so. A could-not-run is never rescued by an allowance.
func TestExitThreeIsAFindingAndTheAllowanceRescuesOnlyAFinding(t *testing.T) {
	f := &fake{rules: []rule{{"govulncheck ./...", "Vulnerability #1: GO-2026-0001\n", 3}}}
	v := check(t.Context(), input(f, "go.mod"))
	if v.State != 1 || !strings.Contains(v.Reason, "go:govulncheck: FINDINGS (exit 1)") || !strings.Contains(v.Reason, "GO-2026-0001") {
		t.Errorf("an unallowed advisory: %+v", v)
	}

	f = &fake{rules: []rule{{"govulncheck ./...", allowedReport, 3}}}
	v = check(t.Context(), input(f, "go.mod"))
	if v.State != 0 || !strings.Contains(v.Reason, "ALLOWED") || !strings.Contains(v.Reason, "not a clean scan") || !strings.Contains(strings.Join(v.Logs, "\n"), "Fixed in") {
		t.Errorf("an allowed advisory: %+v", v)
	}

	f = &fake{rules: []rule{{"govulncheck ./...", allowedReport + "Vulnerability #2: GO-2026-9999\n", 3}}}
	if v = check(t.Context(), input(f, "go.mod")); v.State != 1 {
		t.Errorf("one advisory nobody allowed reds the run: %+v", v)
	}

	f = &fake{rules: []rule{{"govulncheck ./...", allowedReport, 2}}}
	if v = check(t.Context(), input(f, "go.mod")); v.State != 2 {
		t.Errorf("an allowance never rescues a could-not-run: %+v", v)
	}
}

func TestANetworkFaultOnANonZeroExitIsACouldNotRun(t *testing.T) {
	f := &fake{rules: []rule{{"govulncheck ./...", "govulncheck: Get \"https://vuln.go.dev/index/db.json.gz\": dial tcp: connection refused", 1}}}
	v := check(t.Context(), input(f, "go.mod"))
	if v.State != 2 || !strings.Contains(v.Reason, "could not reach its advisory source") {
		t.Errorf("%+v", v)
	}
}

// A step in front of the scan that fails is the atom's could-not-run, never a
// finding about the tree, and the scan is not run.
func TestAFailedProvisioningStepStopsTheScan(t *testing.T) {
	for _, tc := range []struct{ name, needle, want string }{
		{"the download", "go mod download", "preflight `go mod download` exited 1: proxy 502"},
		{"the version probe", "govulncheck -version", "preflight `govulncheck -version` exited 1: proxy 502"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{rules: []rule{{tc.needle, "go: fetching\nproxy 502", 1}}}
			v := check(t.Context(), input(f, "go.mod"))
			if v.State != 2 || !strings.Contains(v.Reason, "go:govulncheck: CANNOT RUN - "+tc.want) {
				t.Errorf("%+v", v)
			}
			for _, r := range f.runs {
				if strings.HasSuffix(r, "govulncheck ./...") {
					t.Errorf("the scan ran after %s failed", tc.name)
				}
			}
		})
	}
}

func TestAScanThatWouldNotStartNeverRan(t *testing.T) {
	f := &fake{rules: []rule{{"govulncheck ./...", "govulncheck: executable file not found", -1}}}
	v := check(t.Context(), input(f, "go.mod"))
	if v.State != 2 || !strings.Contains(v.Reason, "the atom never ran: govulncheck: executable file not found") {
		t.Errorf("%+v", v)
	}
}

// Every module is scanned in its own directory and folded, as the chain did.
func TestEveryModuleIsScannedInItsOwnDirectory(t *testing.T) {
	f := &fake{}
	v := check(t.Context(), input(f, "go.mod", "styx/go.mod", "styx/s.go"))
	if v.State != 0 || !strings.Contains(v.Reason, "go:govulncheck: PASS in 2 Go modules (., styx)") {
		t.Fatalf("%+v", v)
	}
	if len(f.runs) != 6 || f.runs[3] != "/src/styx: go mod download" || f.runs[5] != "/src/styx: govulncheck ./..." {
		t.Errorf("runs %v", f.runs)
	}
}

func TestATreeThatWouldNotEnumerateIsACouldNotRun(t *testing.T) {
	i := input(&fake{})
	i.FilesErr = context.DeadlineExceeded
	if v := check(t.Context(), i); v.State != 2 || !strings.Contains(v.Reason, "could not enumerate the tree's Go modules") {
		t.Errorf("%+v", v)
	}
}
