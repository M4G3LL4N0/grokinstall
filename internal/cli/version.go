package cli

import "runtime/debug"

// Build information. These values are supplied at link time with -ldflags so a
// release binary reports exactly what it was built from, and a development
// build says so honestly rather than pretending to be a release.
var (
	// Version is the semantic version, e.g. v0.1.0.
	Version = "dev"
	// Commit is the git commit the binary was built from.
	Commit = ""
	// BuildDate is when the binary was built (RFC3339).
	BuildDate = ""
)

// BuildInfo is the structured version payload returned by `version --json`.
type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuildDate string `json:"build_date,omitempty"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
	// Development is true when the binary was not stamped by a release build.
	Development bool `json:"development"`
}

// Info returns the build metadata for this binary.
func Info() BuildInfo {
	info := BuildInfo{
		Version:     Version,
		Commit:      Commit,
		BuildDate:   BuildDate,
		GoVersion:   goVersion(),
		Platform:    platform(),
		Development: Version == "dev" || Version == "",
	}
	if info.Development {
		// A development build still reports the commit it was built from when
		// the toolchain recorded it, rather than showing an empty field.
		if info.Commit == "" {
			if bi, ok := debug.ReadBuildInfo(); ok {
				for _, s := range bi.Settings {
					if s.Key == "vcs.revision" {
						info.Commit = s.Value
					}
					if s.Key == "vcs.time" && info.BuildDate == "" {
						info.BuildDate = s.Value
					}
				}
			}
		}
	}
	return info
}
