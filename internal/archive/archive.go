// Package archive extracts upstream release artifacts safely.
//
// Every archive from an untrusted source passes through here, so this package
// is deliberately strict. It rejects path traversal, absolute paths, symlinks
// and hardlinks, and it enforces entry-count, per-file and total-size limits
// while streaming, so a decompression bomb cannot exhaust memory or disk.
package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Default limits for release extraction.
const (
	DefaultMaxEntries    = 5000
	DefaultMaxFileBytes  = 256 << 20 // 256 MiB
	DefaultMaxTotalBytes = 1 << 30   // 1 GiB
)

// Limits bound an extraction.
type Limits struct {
	MaxEntries    int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

func (l Limits) normalized() Limits {
	if l.MaxEntries <= 0 {
		l.MaxEntries = DefaultMaxEntries
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = DefaultMaxFileBytes
	}
	if l.MaxTotalBytes <= 0 {
		l.MaxTotalBytes = DefaultMaxTotalBytes
	}
	return l
}

// Report describes what was actually extracted.
type Report struct {
	Entries    int      `json:"entries"`
	Files      int      `json:"files"`
	TotalBytes int64    `json:"total_bytes"`
	Paths      []string `json:"paths"`
}

// Errors returned by extraction. They name the threat so a diagnosis can
// explain exactly what was blocked and why.
var (
	ErrTraversal   = errors.New("archive path traversal rejected")
	ErrAbsolute    = errors.New("archive absolute path rejected")
	ErrSymlink     = errors.New("archive symlink rejected")
	ErrHardlink    = errors.New("archive hardlink rejected")
	ErrTooMany     = errors.New("archive entry limit exceeded")
	ErrTooLarge    = errors.New("archive size limit exceeded")
	ErrUnsupported = errors.New("unsupported archive format")
)

// Extract unpacks src into dest.
func Extract(src, dest string, limits Limits) (*Report, error) {
	limits = limits.normalized()
	lower := strings.ToLower(src)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGz(src, dest, limits)
	case strings.HasSuffix(lower, ".tar"):
		f, err := os.Open(src)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return extractTar(f, dest, limits)
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(src, dest, limits)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, filepath.Base(src))
	}
}

// safeJoin validates an archive entry name and resolves it inside dest.
func safeJoin(dest, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty entry name", ErrTraversal)
	}
	cleaned := filepath.ToSlash(filepath.Clean(name))
	if strings.HasPrefix(cleaned, "/") || filepath.IsAbs(name) {
		return "", fmt.Errorf("%w: %s", ErrAbsolute, name)
	}
	// A Windows-style drive or UNC path is absolute too.
	if len(name) > 1 && name[1] == ':' {
		return "", fmt.Errorf("%w: %s", ErrAbsolute, name)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%w: %s", ErrTraversal, name)
	}
	target := filepath.Join(dest, filepath.FromSlash(cleaned))
	root, err := filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrTraversal, name)
	}
	return abs, nil
}

func extractTarGz(src, dest string, limits Limits) (*Report, error) {
	f, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("read gzip %s: %w", filepath.Base(src), err)
	}
	defer gz.Close()
	return extractTar(gz, dest, limits)
}

func extractTar(r io.Reader, dest string, limits Limits) (*Report, error) {
	rep := &Report{Paths: []string{}}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		rep.Entries++
		if rep.Entries > limits.MaxEntries {
			return nil, fmt.Errorf("%w: more than %d entries", ErrTooMany, limits.MaxEntries)
		}
		switch hdr.Typeflag {
		case tar.TypeSymlink:
			return nil, fmt.Errorf("%w: %s -> %s", ErrSymlink, hdr.Name, hdr.Linkname)
		case tar.TypeLink:
			return nil, fmt.Errorf("%w: %s -> %s", ErrHardlink, hdr.Name, hdr.Linkname)
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			return nil, fmt.Errorf("%w: special device %s", ErrUnsupported, hdr.Name)
		}

		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return nil, err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, err
			}
		case tar.TypeReg:
			if hdr.Size > limits.MaxFileBytes {
				return nil, fmt.Errorf("%w: %s is %d bytes", ErrTooLarge, hdr.Name, hdr.Size)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return nil, err
			}
			written, err := writeBounded(target, tr, limits.MaxFileBytes, &rep.TotalBytes, limits.MaxTotalBytes)
			if err != nil {
				return nil, err
			}
			_ = written
			rep.Files++
			rep.Paths = append(rep.Paths, hdr.Name)
		default:
			// Unknown entry types are skipped rather than guessed at.
			continue
		}
	}
	return rep, nil
}

func extractZip(src, dest string, limits Limits) (*Report, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return nil, fmt.Errorf("read zip %s: %w", filepath.Base(src), err)
	}
	defer zr.Close()

	rep := &Report{Paths: []string{}}
	for _, f := range zr.File {
		rep.Entries++
		if rep.Entries > limits.MaxEntries {
			return nil, fmt.Errorf("%w: more than %d entries", ErrTooMany, limits.MaxEntries)
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: %s", ErrSymlink, f.Name)
		}
		if f.Mode()&os.ModeType != 0 && !f.FileInfo().IsDir() {
			return nil, fmt.Errorf("%w: special file %s", ErrUnsupported, f.Name)
		}
		target, err := safeJoin(dest, f.Name)
		if err != nil {
			return nil, err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, err
			}
			continue
		}
		if int64(f.UncompressedSize64) > limits.MaxFileBytes {
			return nil, fmt.Errorf("%w: %s is %d bytes", ErrTooLarge, f.Name, f.UncompressedSize64)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			rc.Close()
			return nil, err
		}
		if _, err := writeBounded(target, rc, limits.MaxFileBytes, &rep.TotalBytes, limits.MaxTotalBytes); err != nil {
			rc.Close()
			return nil, err
		}
		rc.Close()
		rep.Files++
		rep.Paths = append(rep.Paths, f.Name)
	}
	return rep, nil
}

// writeBounded copies at most maxFile bytes and never lets the running total
// exceed maxTotal, so a lying header cannot bypass the limit.
func writeBounded(target string, r io.Reader, maxFile int64, total *int64, maxTotal int64) (int64, error) {
	remaining := maxTotal - *total
	if remaining <= 0 {
		return 0, fmt.Errorf("%w: extraction budget exhausted", ErrTooLarge)
	}
	limit := maxFile
	if remaining < limit {
		limit = remaining
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	// Read one byte past the limit so an overflow is detectable.
	written, err := io.Copy(f, io.LimitReader(r, limit+1))
	if err != nil {
		return written, err
	}
	if written > limit {
		_ = os.Remove(target)
		return written, fmt.Errorf("%w: %s exceeds its limit", ErrTooLarge, filepath.Base(target))
	}
	*total += written
	return written, nil
}
