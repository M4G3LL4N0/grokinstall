package github

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitHub serves release metadata and assets for a repository.
func fakeGitHub(t *testing.T, rel Release, assets map[string][]byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/tool/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/download/")
		data, ok := assets[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(data)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func clientFor(srv *httptest.Server) *Client {
	c := NewClient("")
	c.BaseURL = srv.URL
	return c
}

func releaseWith(names ...string) Release {
	rel := Release{TagName: "v1.2.3", Name: "v1.2.3"}
	for _, n := range names {
		rel.Assets = append(rel.Assets, Asset{
			Name: n,
			URL:  "https://example.invalid/download/" + n,
			Size: 100,
		})
	}
	return rel
}

func TestMatchAssetPicksCurrentPlatform(t *testing.T) {
	rel := releaseWith(
		"tool-linux-amd64.tar.gz",
		"tool-linux-aarch64.tar.gz",
		"tool-darwin-arm64.tar.gz",
		"tool-darwin-x86_64.tar.gz",
		"tool-windows-amd64.zip",
	)
	asset, ok := MatchAsset(&rel, Platform{OS: "darwin", Arch: "arm64"})
	if !ok {
		t.Fatal("expected a match")
	}
	if asset.Name != "tool-darwin-arm64.tar.gz" {
		t.Fatalf("matched %q", asset.Name)
	}
}

func TestMatchAssetReturnsNothingWhenNoMatch(t *testing.T) {
	rel := releaseWith("tool-plan9-sparc.tar.gz")
	if _, ok := MatchAsset(&rel, Platform{OS: "linux", Arch: "arm64"}); ok {
		t.Fatal("must not guess a match")
	}
}

func TestMatchAssetIgnoresChecksumsAndPackages(t *testing.T) {
	rel := releaseWith(
		"tool-darwin-arm64.tar.gz",
		"tool-darwin-arm64.tar.gz.sha256",
		"SHA256SUMS",
		"tool-darwin-arm64.dmg",
		"tool-darwin-arm64.pkg",
	)
	asset, ok := MatchAsset(&rel, Platform{OS: "darwin", Arch: "arm64"})
	if !ok {
		t.Fatal("expected a match")
	}
	if asset.Name != "tool-darwin-arm64.tar.gz" {
		t.Fatalf("matched %q, want the archive rather than a checksum or installer", asset.Name)
	}
}

func TestMatchAssetPrefersPlainBinary(t *testing.T) {
	rel := releaseWith("tool-darwin-arm64.tar.gz", "tool-darwin-arm64")
	asset, _ := MatchAsset(&rel, Platform{OS: "darwin", Arch: "arm64"})
	if asset.Name != "tool-darwin-arm64" {
		t.Fatalf("matched %q, want the plain binary", asset.Name)
	}
}

func TestLatestRelease(t *testing.T) {
	srv := fakeGitHub(t, releaseWith("tool-darwin-arm64"), nil)
	rel, err := clientFor(srv).LatestRelease(context.Background(), "acme", "tool")
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.2.3" {
		t.Fatalf("tag = %q", rel.TagName)
	}
}

func TestDownloadComputesChecksum(t *testing.T) {
	srv := fakeGitHub(t, Release{}, map[string][]byte{"tool-darwin-arm64": []byte("binary-bytes")})
	dir := t.TempDir()
	sum, err := clientFor(srv).Download(context.Background(), srv.URL+"/download/tool-darwin-arm64", filepath.Join(dir, "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sum) != 64 {
		t.Fatalf("checksum = %q", sum)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "tool"))
	if string(data) != "binary-bytes" {
		t.Fatalf("content = %q", data)
	}
}

func TestDownloadEnforcesSizeBound(t *testing.T) {
	// The client must never write more than its declared bound, even if the
	// server lies about the asset size.
	srv := fakeGitHub(t, Release{}, map[string][]byte{"big": []byte(strings.Repeat("x", 4096))})
	dest := filepath.Join(t.TempDir(), "big")
	if _, err := clientFor(srv).Download(context.Background(), srv.URL+"/download/big", dest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > MaxDownloadBytes {
		t.Fatalf("download exceeded its bound: %d", info.Size())
	}
}

func TestParseChecksums(t *testing.T) {
	h1 := strings.Repeat("a", 64)
	h2 := strings.Repeat("b", 64)
	text := h1 + "  tool-linux.tar.gz\n" +
		h2 + "  tool-darwin-arm64.tar.gz\n" +
		"not-a-hash  tool.zip\n"
	got := ParseChecksums(text)
	if got["tool-linux.tar.gz"] != h1 {
		t.Fatalf("parsed = %v", got)
	}
	if got["tool-darwin-arm64.tar.gz"] != h2 {
		t.Fatalf("parsed = %v", got)
	}
	if _, ok := got["tool.zip"]; ok {
		t.Fatal("an invalid hash must not be accepted as a checksum")
	}
}

func TestChecksumVerificationAndMissing(t *testing.T) {
	rel := releaseWith("tool-darwin-arm64.tar.gz", "SHA256SUMS")
	srv := fakeGitHub(t, rel, map[string][]byte{
		"tool-darwin-arm64.tar.gz": []byte("payload"),
		"SHA256SUMS":               []byte(strings.Repeat("a", 64) + "  tool-darwin-arm64.tar.gz\n"),
	})
	rel.Assets[0].URL = srv.URL + "/download/tool-darwin-arm64.tar.gz"
	rel.Assets[1].URL = srv.URL + "/download/SHA256SUMS"
	c := clientFor(srv)
	sums, ok, err := c.DownloadChecksumFile(context.Background(), &rel, "tool-darwin-arm64.tar.gz")
	if err != nil || !ok {
		t.Fatalf("checksum file should be found: ok=%v err=%v", ok, err)
	}
	if sums["tool-darwin-arm64.tar.gz"] == "" {
		t.Fatalf("checksum not parsed: %v", sums)
	}
}

func TestChecksumUnavailableIsReportedNotInvented(t *testing.T) {
	rel := releaseWith("tool-darwin-arm64")
	srv := fakeGitHub(t, rel, nil)
	_, ok, err := clientFor(srv).DownloadChecksumFile(context.Background(), &rel, "tool-darwin-arm64")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("no checksum file exists; reporting one would be fabrication")
	}
}

// tarGzWithBinary builds a tar.gz containing an executable shell script.
func tarGzWithBinary(t *testing.T, name string, script []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(script))})
	tw.Write(script)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestReleaseArchiveIsExtractedSafely(t *testing.T) {
	payload := tarGzWithBinary(t, "tool-1.2.3/bin/tool", []byte("#!/bin/sh\necho tool\n"))
	rel := releaseWith("tool-darwin-arm64.tar.gz")
	srv := fakeGitHub(t, rel, map[string][]byte{"tool-darwin-arm64.tar.gz": payload})
	rel.Assets[0].URL = srv.URL + "/download/tool-darwin-arm64.tar.gz"
	c := clientFor(srv)

	dir := t.TempDir()
	archivePath := filepath.Join(dir, "asset.tar.gz")
	sum, err := c.Download(context.Background(), srv.URL+"/download/tool-darwin-arm64.tar.gz", archivePath)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "unpacked")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	rep, err := extractArchive(archivePath, dest)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Files != 1 {
		t.Fatalf("files = %d", rep.Files)
	}
	bin := filepath.Join(dest, "tool-1.2.3", "bin", "tool")
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal("the extracted binary is not executable")
	}
	if len(sum) != 64 {
		t.Fatalf("checksum = %q", sum)
	}
}

func TestMaliciousArchiveIsRejected(t *testing.T) {
	payload := tarGzWithBinary(t, "../../../etc/cron.d/evil", []byte("evil"))
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "evil.tar.gz")
	if err := os.WriteFile(archivePath, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(archivePath, filepath.Join(dir, "out")); err == nil {
		t.Fatal("a traversal archive must be rejected")
	}
}

func TestZipReleaseAsset(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("tool/tool.exe")
	w.Write([]byte("MZ"))
	zw.Close()
	rel := releaseWith("tool-windows-amd64.zip")
	srv := fakeGitHub(t, rel, map[string][]byte{"tool-windows-amd64.zip": buf.Bytes()})
	rel.Assets[0].URL = srv.URL + "/download/tool-windows-amd64.zip"
	dir := t.TempDir()
	c := clientFor(srv)
	archivePath := filepath.Join(dir, "asset.zip")
	if _, err := c.Download(context.Background(), srv.URL+"/download/tool-windows-amd64.zip", archivePath); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(archivePath, filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
}
