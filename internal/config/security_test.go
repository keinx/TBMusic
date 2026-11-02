package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSaveUsesPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := Default()
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("exposed config mode: %o", info.Mode().Perm())
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
}
