package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Ownership says who controls an execution target.
type Ownership string

// Ownership levels.
const (
	// OwnershipNone is the zero value: nothing is known about the target, so it
	// is treated conservatively by the caller that builds the Trust value.
	OwnershipNone Ownership = ""
	// OwnershipGrokinstall means GrokInstall provisioned the executable into a
	// runtime directory it controls. These are hash-enforced.
	OwnershipGrokinstall Ownership = "grokinstall"
	// OwnershipExternal means the executable already existed on the machine.
	// GrokInstall references it and makes no claim about restoring it.
	OwnershipExternal Ownership = "external"
	// OwnershipSupplied means the user pointed GrokInstall at an executable.
	OwnershipSupplied Ownership = "user_supplied"
)

// Trust describes how much must be verified about an execution target before it
// is spawned.
//
// The distinction that matters: GrokInstall can only promise the bytes it
// installed. For a runtime it provisioned, integrity is a hard gate. For an
// executable that already existed on the machine, a routine package upgrade
// changes the file, and treating that as an attack would make GrokInstall cry
// wolf on `brew upgrade`. External targets are therefore checked for existence
// and recorded as external, not hash-enforced.
type Trust struct {
	// Ownership is who controls the execution target.
	Ownership Ownership
	// Command is the executable that will be spawned.
	Command string
	// RuntimeDir is the GrokInstall-owned directory holding the executable,
	// used to resolve recorded relative hashes.
	RuntimeDir string
	// Recorded holds hashes captured at install time, keyed by path relative to
	// RuntimeDir.
	Recorded map[string]string
	// Hasher hashes a file. Injected so the gate is testable without disk and
	// so hashing happens exactly once per file.
	Hasher func(path string) (string, error)
	// Stat reports whether a path exists as a file. Injected for the same
	// reason.
	Stat func(path string) (bool, error)
}

// defaultHasher hashes a file with SHA-256.
func defaultHasher(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func defaultStat(path string) (bool, error) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return !st.IsDir(), nil
}

// Verify applies the trust gate and returns a structured failure when the
// execution target must not be spawned, or nil when it is safe to proceed.
//
// It runs before any process is created. That ordering is the whole point: a
// capability whose owned runtime was modified must never be executed first and
// warned about afterwards.
//
// Deliberately absent: any repair. This function reads and compares; restoring
// a modified executable is a separate, explicit operation.
func Verify(t Trust, capability string) *Error {
	if strings.TrimSpace(t.Command) == "" {
		return &Error{
			Code:      CodeNotExecutable,
			Message:   "no execution target is registered for this capability",
			Component: "execution",
		}
	}
	hasher := t.Hasher
	if hasher == nil {
		hasher = defaultHasher
	}
	stat := t.Stat
	if stat == nil {
		stat = defaultStat
	}

	exists, err := stat(t.Command)
	if err != nil || !exists {
		return &Error{
			Code:              CodeNotExecutable,
			Message:           "the execution target is missing: " + t.Command,
			Capability:        capability,
			Component:         "runtime",
			RecommendedAction: "grokinstall diagnose " + capability,
		}
	}

	// Only GrokInstall-owned runtimes are hash-enforced. An external executable
	// belongs to the user: GrokInstall neither owns it nor can restore it, and a
	// normal upgrade must not read as tampering.
	if t.Ownership != OwnershipGrokinstall {
		return nil
	}
	if len(t.Recorded) == 0 {
		return &Error{
			Code:              CodeIntegrityFailure,
			Message:           "no recorded integrity hashes exist for a GrokInstall-owned runtime, so it cannot be trusted",
			Capability:        capability,
			Component:         "runtime",
			RecommendedAction: "grokinstall diagnose " + capability,
		}
	}

	paths := make([]string, 0, len(t.Recorded))
	for p := range t.Recorded {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var modified, missing []string
	for _, rel := range paths {
		full := rel
		if t.RuntimeDir != "" && !filepath.IsAbs(rel) {
			full = filepath.Join(t.RuntimeDir, rel)
		}
		ok, statErr := stat(full)
		if statErr != nil || !ok {
			missing = append(missing, rel)
			continue
		}
		actual, hashErr := hasher(full)
		if hashErr != nil {
			missing = append(missing, rel)
			continue
		}
		if actual != t.Recorded[rel] {
			modified = append(modified, rel)
		}
	}

	switch {
	case len(modified) > 0:
		return &Error{
			Code:              CodeIntegrityFailure,
			Message:           "capability runtime differs from the verified installed artifact: " + strings.Join(modified, ", "),
			Details:           "recorded hashes were compared against the files on disk; execution is blocked and nothing was repaired",
			Capability:        capability,
			Component:         "runtime",
			RecommendedAction: "grokinstall diagnose " + capability,
		}
	case len(missing) > 0:
		return &Error{
			Code:              CodeIntegrityFailure,
			Message:           "capability runtime is missing verified files: " + strings.Join(missing, ", "),
			Capability:        capability,
			Component:         "runtime",
			RecommendedAction: "grokinstall diagnose " + capability,
		}
	}
	return nil
}
