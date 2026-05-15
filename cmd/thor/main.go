// Command thor is the partner-facing CLI entrypoint for the Thor PSA Validator.
package main

import (
	"os"

	"thor-golang/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
