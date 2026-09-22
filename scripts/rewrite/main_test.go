package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	hit := filepath.Join(dir, "hit.sh")
	miss := filepath.Join(dir, "miss.txt")

	if err := os.WriteFile(hit, []byte("a.b/x a.b/x axb/x\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(miss, []byte("nothing\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := run(strings.NewReader(hit+"\x00"+miss+"\x00"), "a.b", "c&d"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(hit)
	if err != nil {
		t.Fatal(err)
	}

	if want := "c&d/x c&d/x axb/x\n"; string(got) != want {
		t.Errorf("content = %q, want %q", got, want)
	}

	info, err := os.Stat(hit)
	if err != nil {
		t.Fatal(err)
	}

	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Errorf("perm = %o, want 755", perm)
	}
}

func TestRunEmptyOld(t *testing.T) {
	t.Parallel()

	if err := run(strings.NewReader(""), "", "x"); err == nil {
		t.Error("want error for empty OLD")
	}
}
