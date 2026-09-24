package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTarGz creates a tar.gz from a map of path -> content.
func buildTarGz(t *testing.T, files map[string]string, dirs []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, d := range dirs {
		if err := tw.WriteHeader(&tar.Header{Name: d, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeArchive(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractTarGz(t *testing.T) {
	dir := t.TempDir()
	src := writeArchive(t, dir, "a.tar.gz", buildTarGz(t, map[string]string{
		"bin/tool": "#!/bin/sh\necho hi\n",
	}, []string{"bin/"}))
	dest := t.TempDir()
	rep, err := Extract(src, dest, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Files != 1 {
		t.Fatalf("files = %d, want 1", rep.Files)
	}
	data, err := os.ReadFile(filepath.Join(dest, "bin", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "#!/bin/sh\necho hi\n" {
		t.Fatalf("content mismatch: %q", data)
	}
}

func TestExtractZip(t *testing.T) {
	dir := t.TempDir()
	src := writeArchive(t, dir, "a.zip", buildZip(t, map[string]string{"bin/tool": "x"}))
	dest := t.TempDir()
	rep, err := Extract(src, dest, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Files != 1 {
		t.Fatalf("files = %d", rep.Files)
	}
}

func TestRejectsParentTraversal(t *testing.T) {
	dir := t.TempDir()
	src := writeArchive(t, dir, "evil.tar.gz", buildTarGz(t, map[string]string{
		"../../etc/passwd": "pwned",
	}, nil))
	dest := t.TempDir()
	_, err := Extract(src, dest, Limits{})
	if err == nil {
		t.Fatal("path traversal must be rejected")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Fatalf("error should name the threat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "etc")); err == nil {
		t.Fatal("traversal wrote outside the destination")
	}
}

func TestRejectsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	src := writeArchive(t, dir, "abs.tar.gz", buildTarGz(t, map[string]string{
		"/etc/cron.d/evil": "pwned",
	}, nil))
	_, err := Extract(src, t.TempDir(), Limits{})
	if err == nil {
		t.Fatal("absolute paths must be rejected")
	}
}

func TestRejectsSymlinkEscape(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// A symlink pointing outside the destination.
	tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o777})
	// And a file written through it.
	tw.WriteHeader(&tar.Header{Name: "link/evil", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4})
	tw.Write([]byte("evil"))
	tw.Close()
	gz.Close()
	dir := t.TempDir()
	src := writeArchive(t, dir, "link.tar.gz", buf.Bytes())
	dest := t.TempDir()
	_, err := Extract(src, dest, Limits{})
	if err == nil {
		t.Fatal("symlink escape must be rejected")
	}
	if _, err := os.Stat("/etc/passwd.evil"); err == nil {
		t.Fatal("symlink escape wrote outside the destination")
	}
}

func TestRejectsSymlinkEntry(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "sneaky", Typeflag: tar.TypeSymlink, Linkname: "target", Mode: 0o777})
	tw.Close()
	gz.Close()
	dir := t.TempDir()
	src := writeArchive(t, dir, "sym.tar.gz", buf.Bytes())
	dest := t.TempDir()
	_, err := Extract(src, dest, Limits{})
	if err == nil {
		t.Fatal("symlink entries must be rejected by default")
	}
	if _, err := os.Lstat(filepath.Join(dest, "sneaky")); err == nil {
		t.Fatal("symlink was created")
	}
}

func TestRejectsHardlinkEscape(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "hard", Typeflag: tar.TypeLink, Linkname: "/etc/passwd", Mode: 0o644})
	tw.Close()
	gz.Close()
	dir := t.TempDir()
	src := writeArchive(t, dir, "hard.tar.gz", buf.Bytes())
	_, err := Extract(src, t.TempDir(), Limits{})
	if err == nil {
		t.Fatal("hardlink entries must be rejected")
	}
}

func TestRejectsTooManyEntries(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 50; i++ {
		files["f"+itoa(i)] = "x"
	}
	dir := t.TempDir()
	src := writeArchive(t, dir, "many.tar.gz", buildTarGz(t, files, nil))
	_, err := Extract(src, t.TempDir(), Limits{MaxEntries: 10})
	if err == nil {
		t.Fatal("entry-count limit must be enforced")
	}
	if !strings.Contains(err.Error(), "entries") {
		t.Fatalf("error should mention entries: %v", err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestRejectsOversizedArchive(t *testing.T) {
	dir := t.TempDir()
	// 200 KB of zeros compresses tiny but expands past the limit: a gzip bomb.
	big := make([]byte, 200*1024)
	src := writeArchive(t, dir, "bomb.tar.gz", buildTarGz(t, map[string]string{"big.bin": string(big)}, nil))
	_, err := Extract(src, t.TempDir(), Limits{MaxTotalBytes: 1024})
	if err == nil {
		t.Fatal("expanded size limit must be enforced")
	}
	if !strings.Contains(err.Error(), "size") {
		t.Fatalf("error should mention size: %v", err)
	}
}

func TestRejectsOversizedSingleFile(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, 10*1024)
	src := writeArchive(t, dir, "big.tar.gz", buildTarGz(t, map[string]string{"big.bin": string(big)}, nil))
	_, err := Extract(src, t.TempDir(), Limits{MaxFileBytes: 1024})
	if err == nil {
		t.Fatal("per-file limit must be enforced")
	}
}

func TestRejectsUnknownFormat(t *testing.T) {
	dir := t.TempDir()
	src := writeArchive(t, dir, "mystery.bin", []byte("not an archive"))
	_, err := Extract(src, t.TempDir(), Limits{})
	if err == nil {
		t.Fatal("unknown archive formats must be rejected rather than guessed")
	}
}

func TestReportIsBoundedAndHonest(t *testing.T) {
	dir := t.TempDir()
	src := writeArchive(t, dir, "a.tar.gz", buildTarGz(t, map[string]string{
		"bin/tool": "x",
		"README":   "y",
	}, []string{"bin/"}))
	rep, err := Extract(src, t.TempDir(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.TotalBytes != 2 {
		t.Fatalf("total bytes = %d, want 2", rep.TotalBytes)
	}
	if len(rep.Paths) != 2 {
		t.Fatalf("paths = %v", rep.Paths)
	}
}

func TestNestedZipIsStillBounded(t *testing.T) {
	// A zip entry that is itself a zip bomb: nested extraction must stay bounded.
	inner := buildZip(t, map[string]string{"payload": strings.Repeat("A", 100000)})
	outer := buildZip(t, map[string]string{"inner.zip": string(inner)})
	dir := t.TempDir()
	src := writeArchive(t, dir, "nested.zip", outer)
	rep, err := Extract(src, t.TempDir(), Limits{MaxTotalBytes: 50000})
	if err != nil {
		t.Fatal(err)
	}
	// The inner zip is just a file; it is not recursively expanded.
	if rep.TotalBytes > 50000 {
		t.Fatalf("nested archive expanded beyond the limit: %d", rep.TotalBytes)
	}
}

func TestExtractIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	data := buildTarGz(t, map[string]string{"b": "2", "a": "1", "c": "3"}, nil)
	src := writeArchive(t, dir, "det.tar.gz", data)
	first, err := Extract(src, t.TempDir(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Extract(src, t.TempDir(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(first.Paths, ",") != strings.Join(second.Paths, ",") {
		t.Fatalf("extraction order is not deterministic: %v vs %v", first.Paths, second.Paths)
	}
}
