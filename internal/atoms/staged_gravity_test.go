package atoms

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStagedGravityIsTheFileGRAVITYNames(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "gravity-x")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GRAVITY", bin)
	path, set, err := StagedGravity("aeb03bfd4a6f14e29b1a91dcdfad05e02695db39")
	if err != nil || path != bin || set != "" {
		t.Errorf("%q %q %v", path, set, err)
	}
}
