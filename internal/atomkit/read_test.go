package atomkit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRaw(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUT_444_ReadVersionChecked(t *testing.T) {
	rp := writeRaw(t, "run-plan.json", `{"version":2}`+"\n")
	_, err := ReadRunPlan(rp)
	if err == nil || !strings.Contains(err.Error(), rp) || !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("ReadRunPlan: %v", err)
	}
	var ve *VersionError
	if !errors.As(err, &ve) || ve.Got != 2 {
		t.Fatalf("not a VersionError{2}: %v", err)
	}
	tp := writeRaw(t, "tree.json", `{"version":2}`+"\n")
	_, err = ReadTree(tp)
	if err == nil || !strings.Contains(err.Error(), tp) || !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("ReadTree: %v", err)
	}
	cp := writeRaw(t, "go.json", `{"version":0}`+"\n")
	if _, err = ReadContainerPlan(cp); err == nil || !strings.Contains(err.Error(), "version 0") {
		t.Fatalf("ReadContainerPlan: %v", err)
	}
}

func TestUT_445_ReadToleratesUnknownFields(t *testing.T) {
	b, _ := Marshal(TF1())
	body := strings.Replace(string(b), `"version":1,`, `"version":1,"zz":1,`, 1)
	got, err := ReadTree(writeRaw(t, "tree.json", body))
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != "/src" || len(got.Files) != 2 {
		t.Fatalf("decoded wrong: %+v", got)
	}
}

func TestReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "rp.json")
	if err := WriteFile(rp, RP1()); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRunPlan(rp)
	if err != nil {
		t.Fatal(err)
	}
	if got.Repo != "rob/nyx" || len(got.Atoms) != 2 || got.Rider != nil {
		t.Fatalf("RunPlan: %+v", got)
	}
	cp := filepath.Join(dir, "cp.json")
	if err := WriteFile(cp, ContainerPlan{Version: 1, Container: "go", MaxParallel: 2, Preflight: []string{}, Atoms: []PlanEntry{PEVET()}}); err != nil {
		t.Fatal(err)
	}
	c, err := ReadContainerPlan(cp)
	if err != nil || c.Atoms[0].ID != "go:vet" {
		t.Fatalf("ContainerPlan: %+v %v", c, err)
	}
}

func TestReadErrorsNameTheFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.json")
	for name, fn := range map[string]func() error{
		"run-plan":       func() error { _, err := ReadRunPlan(missing); return err },
		"container-plan": func() error { _, err := ReadContainerPlan(missing); return err },
		"tree":           func() error { _, err := ReadTree(missing); return err },
	} {
		if err := fn(); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := writeRaw(t, "bad.json", "{")
	if _, err := ReadTree(bad); err == nil || !strings.Contains(err.Error(), bad) {
		t.Errorf("garbage: %v", err)
	}
}
