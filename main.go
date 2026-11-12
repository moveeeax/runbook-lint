// Command runbook-lint checks operational runbooks for the structure and
// metadata you want to already be there when an incident starts.
package main

import (
	"os"

	"github.com/moveeeax/runbook-lint/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
