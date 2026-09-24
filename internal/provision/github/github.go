// Package github provisions an executable from a public GitHub release.
//
// The provisioner is deliberately narrow: it only reads release metadata, only
// downloads assets that match this machine, and only reports a checksum as
// verified when upstream actually published one. When upstream publishes no
// checksum, the result says so rather than implying verification it did not do.
package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"grokinstall/internal/archive"
)

// Default bounds for a release artifact.
const (
	MaxDownloadBytes = 300 << 20 // 300 MiB
	DownloadTimeout  = 5 * time.Minute
)

// Platform describes the current machine.
type Platform struct {
	OS   string
	Arch string
}

// CurrentPlatform detects the running platform mechanically.
func CurrentPlatform() Platform {
	return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// Asset is one downloadable release artifact.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release is the subset of release metadata used here.
type Release struct {
	TagName     string  `json:"tag_name"`
	Name        string  `json:"name"`
	Draft       bool    `json:"draft"`
	Prerelease  bool    `json:"prerelease"`
	PublishedAt string  `json:"published_at"`
	Assets      []Asset `json:"assets"`
}

// Client talks to the public GitHub API.
type Client struct {
	HTTP  *http.Client
	Token string
	// BaseURL allows tests to point at a local server.
	BaseURL string
}

// NewClient builds a client with bounded timeouts.
func NewClient(token string) *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: DownloadTimeout},
		Token:   token,
		BaseURL: "https://api.github.com",
	}
}

func (c *Client) url(path string) string {
	base := c.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	return base + path
}

func (c *Client) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "grokinstall")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(req)
}

// LatestRelease fetches the latest published, non-draft, non-prerelease release.
func (c *Client) LatestRelease(ctx context.Context, owner, repo string) (*Release, error) {
	resp, err := c.get(ctx, c.url("/repos/"+owner+"/"+repo+"/releases/latest"))
	if err != nil {
		return nil, fmt.Errorf("fetch release metadata for %s/%s: %w", owner, repo, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch release metadata for %s/%s: http %d", owner, repo, resp.StatusCode)
	}
	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release metadata: %w", err)
	}
	return &rel, nil
}

// platformGroups returns alternative token groups. An asset matches when it
// contains at least one token from the OS group and at least one from the arch
// group. These are alternatives, not a conjunction: no release is named
// "darwin-macos-aarch64-arm64".
func platformGroups(p Platform) (osGroup, archGroup, penalized []string) {
	switch p.OS {
	case "darwin":
		osGroup = []string{"darwin", "macos", "apple-darwin", "osx", "mac"}
		penalized = []string{"linux", "windows", "freebsd", "netbsd", "openbsd"}
	case "linux":
		osGroup = []string{"linux"}
		penalized = []string{"darwin", "macos", "windows", "freebsd"}
	case "windows":
		osGroup = []string{"windows", "win64", "win32", "-win", "win-"}
		penalized = []string{"darwin", "macos", "linux", "freebsd"}
	default:
		osGroup = []string{p.OS}
	}
	switch p.Arch {
	case "arm64":
		archGroup = []string{"aarch64", "arm64", "armv8"}
	default:
		archGroup = []string{"x86_64", "amd64", "x64", "64bit"}
	}
	return osGroup, archGroup, penalized
}

func containsAny(lower string, tokens []string) bool {
	for _, t := range tokens {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

// MatchAsset selects a compatible asset conservatively. It returns nil rather
// than guessing when no asset clearly matches this platform.
func MatchAsset(rel *Release, p Platform) (Asset, bool) {
	if rel == nil || len(rel.Assets) == 0 {
		return Asset{}, false
	}
	osGroup, archGroup, penalized := platformGroups(p)

	// Checksum and installer files are never the executable we want.
	isChecksum := func(name string) bool {
		lower := strings.ToLower(name)
		for _, marker := range []string{"sha256sum", "checksums", ".sha256", ".sha512", ".sig", ".asc", ".pem", ".deb", ".rpm", ".apk", ".dmg", ".pkg", ".msi", "sbom"} {
			if strings.Contains(lower, marker) {
				return true
			}
		}
		return false
	}

	var matches []Asset
	for _, a := range rel.Assets {
		lower := strings.ToLower(a.Name)
		if isChecksum(lower) {
			continue
		}
		if !containsAny(lower, osGroup) || !containsAny(lower, archGroup) {
			continue
		}
		if containsAny(lower, penalized) {
			continue
		}
		matches = append(matches, a)
	}
	if len(matches) == 0 {
		return Asset{}, false
	}
	// Prefer the plain binary, then the smallest archive, deterministically.
	sort.SliceStable(matches, func(i, j int) bool {
		bi, bj := isPlainBinary(matches[i].Name), isPlainBinary(matches[j].Name)
		if bi != bj {
			return bi
		}
		if matches[i].Size != matches[j].Size {
			return matches[i].Size < matches[j].Size
		}
		return matches[i].Name < matches[j].Name
	})
	return matches[0], true
}

func isPlainBinary(name string) bool {
	lower := strings.ToLower(name)
	return !strings.HasSuffix(lower, ".tar.gz") && !strings.HasSuffix(lower, ".tgz") &&
		!strings.HasSuffix(lower, ".zip") && !strings.HasSuffix(lower, ".tar.xz") &&
		!strings.HasSuffix(lower, ".tar.bz2")
}

// Download fetches an asset into dir with a hard size bound.
func (c *Client) Download(ctx context.Context, url, dest string) (string, error) {
	resp, err := c.get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: http %d", filepath.Base(url), resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()
	limited := io.LimitReader(resp.Body, MaxDownloadBytes+1)
	sum := sha256.New()
	written, err := io.Copy(io.MultiWriter(f, sum), limited)
	if err != nil {
		return "", err
	}
	if written > MaxDownloadBytes {
		_ = os.Remove(dest)
		return "", fmt.Errorf("download exceeded the %d byte limit", MaxDownloadBytes)
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// DownloadChecksumFile fetches an upstream checksum manifest, if present.
func (c *Client) DownloadChecksumFile(ctx context.Context, rel *Release, assetName string) (map[string]string, bool, error) {
	for _, a := range rel.Assets {
		lower := strings.ToLower(a.Name)
		if !strings.Contains(lower, "sha256") && !strings.Contains(lower, "checksums") {
			continue
		}
		resp, err := c.get(ctx, a.URL)
		if err != nil {
			return nil, false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, false, nil
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return nil, false, err
		}
		return ParseChecksums(string(data)), true, nil
	}
	return nil, false, nil
}

// ParseChecksums reads a common `sha256  filename` manifest format.
func ParseChecksums(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sum := strings.ToLower(fields[0])
		if len(sum) != 64 {
			continue
		}
		if _, err := hex.DecodeString(sum); err != nil {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		out[filepath.Base(name)] = sum
	}
	return out
}

// FileChecksum hashes a file on disk.
func FileChecksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// ExtractionLimits are the bounds used when unpacking a release asset.
func ExtractionLimits() archive.Limits {
	return archive.Limits{
		MaxEntries:    2000,
		MaxFileBytes:  512 << 20,
		MaxTotalBytes: 1 << 30,
	}
}

// extractArchive unpacks a downloaded release asset under the archive safety
// rules.
func extractArchive(path, dest string) (*archive.Report, error) {
	return archive.Extract(path, dest, ExtractionLimits())
}

// ExtractArchive exposes safe extraction for provisioners.
func ExtractArchive(path, dest string) (*archive.Report, error) {
	return extractArchive(path, dest)
}
