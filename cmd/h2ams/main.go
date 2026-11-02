// Command h2ams manages and supervises local applications.
package main

import (
	"fmt"
	"os"

	"github.com/yvyty/h2ams/internal/cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
