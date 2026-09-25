package cli

import "runtime"

func goVersion() string { return runtime.Version() }

func platform() string { return runtime.GOOS + "/" + runtime.GOARCH }
