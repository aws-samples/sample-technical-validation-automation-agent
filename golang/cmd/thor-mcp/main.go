// Command thor-mcp is the stdio MCP server entrypoint for Thor.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"thor-golang/internal/mcpserver"
	"thor-golang/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return
	}

	if err := mcpserver.Run(context.Background(), mcpserver.Options{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
