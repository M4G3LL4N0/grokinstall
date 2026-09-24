// Command grokinstall is the GrokInstall CLI.
package main

import (
	"fmt"
	"os"

	"grokinstall/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "grokinstall:", err)
		os.Exit(1)
	}
}
