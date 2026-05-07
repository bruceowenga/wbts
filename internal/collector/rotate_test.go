package collector

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// writeGzip creates a gzip-compressed file at path containing content.
func writeGzip(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	if _, err := gz.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRotatedPaths_NumberedAndGz(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "auth.log")

	// Create: auth.log, auth.log.1, auth.log.2.gz
	for _, name := range []string{"auth.log", "auth.log.1"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeGzip(t, filepath.Join(dir, "auth.log.2.gz"), "x")

	paths := rotatedPaths(base)

	if len(paths) != 3 {
		t.Fatalf("got %d paths, want 3: %v", len(paths), paths)
	}
	// Oldest first: auth.log.2.gz, auth.log.1, auth.log
	if filepath.Base(paths[0]) != "auth.log.2.gz" {
		t.Errorf("expected auth.log.2.gz first (oldest), got %s", filepath.Base(paths[0]))
	}
	if filepath.Base(paths[len(paths)-1]) != "auth.log" {
		t.Errorf("expected auth.log last (newest), got %s", filepath.Base(paths[len(paths)-1]))
	}
}

func TestRotatedPaths_DateBased(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "dnf.rpm.log")

	os.WriteFile(filepath.Join(dir, "dnf.rpm.log"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "dnf.rpm.log-20260401"), []byte("x"), 0644)
	writeGzip(t, filepath.Join(dir, "dnf.rpm.log-20260315.gz"), "x")

	paths := rotatedPaths(base)

	if len(paths) != 3 {
		t.Fatalf("got %d paths, want 3: %v", len(paths), paths)
	}
	// Date-based are sorted lexicographically (YYYYMMDD = chronological)
	// Oldest: dnf.rpm.log-20260315.gz, then dnf.rpm.log-20260401, then dnf.rpm.log
	if filepath.Base(paths[0]) != "dnf.rpm.log-20260315.gz" {
		t.Errorf("expected oldest date first, got %s", filepath.Base(paths[0]))
	}
	if filepath.Base(paths[len(paths)-1]) != "dnf.rpm.log" {
		t.Errorf("expected active log last, got %s", filepath.Base(paths[len(paths)-1]))
	}
}

func TestRotatedPaths_MissingFileSkipped(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "auth.log")
	os.WriteFile(base, []byte("x"), 0644)
	// auth.log.1 does NOT exist — should be skipped silently

	paths := rotatedPaths(base)
	if len(paths) != 1 {
		t.Errorf("got %d paths, want 1 (only active log)", len(paths))
	}
}

func TestMultiFileReader_PlainAndGzip(t *testing.T) {
	dir := t.TempDir()

	plain := filepath.Join(dir, "plain.log")
	gz := filepath.Join(dir, "old.log.gz")

	os.WriteFile(plain, []byte("line from plain\n"), 0644)
	writeGzip(t, gz, "line from gz\n")

	r, closers, err := multiFileReader([]string{gz, plain})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()

	content, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(content, []byte("line from plain")) {
		t.Error("plain file content missing")
	}
	if !bytes.Contains(content, []byte("line from gz")) {
		t.Error("gzip file content missing")
	}
}

func TestMultiFileReader_UnreadableSkipped(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "auth.log")
	os.WriteFile(plain, []byte("line1\n"), 0644)

	// nonexistent file should be silently skipped
	r, closers, err := multiFileReader([]string{"/nonexistent/missing.log", plain})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()

	content, _ := io.ReadAll(r)
	if !bytes.Contains(content, []byte("line1")) {
		t.Error("readable file content missing after skipping unreadable")
	}
}
