// Package mcpserver wires an MCP server that exposes the Thor tool suite.
// cmd/thor-mcp is a five-line entrypoint that calls Run.
//
// Hard rule: this package never writes to os.Stdout — the MCP stdio
// transport carries protocol JSON there and any non-protocol bytes
// corrupt the connection. All operator log output goes to stderr via
// runlog.NewLogger.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"thor-golang/internal/runlog"
	"thor-golang/internal/version"
)

// ServerName is the MCP advertised name. Mirrors Python `server` constructor.
const ServerName = "thor-psa-validator"

// Options configures Run. All fields default sensibly; tests override
// Transport (with mcp.InMemoryTransport) and the validator factory.
type Options struct {
	// Transport is the MCP transport to bind. Defaults to stdio.
	Transport mcp.Transport
	// Stderr is the operator-log destination. Defaults to os.Stderr.
	Stderr io.Writer
	// Validator wires the live validator factory. Tests inject a fake.
	Validator ValidatorFactory
}

// Run starts the MCP server and blocks until the transport closes or
// the supplied ctx is cancelled. ctx is also wired to OS signals
// (SIGINT/SIGTERM) for graceful shutdown — pass context.Background()
// from main.
func Run(ctx context.Context, opts Options) error {
	if opts.Transport == nil {
		opts.Transport = &mcp.StdioTransport{}
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Validator == nil {
		opts.Validator = ProductionValidatorFactory
	}

	logger := runlog.NewLogger(runlog.Options{Writer: opts.Stderr})
	logger.Info("thor-mcp starting", "version", version.Short())

	srv := newServer()
	registerTools(srv, opts, logger)

	// Graceful shutdown: SIGINT/SIGTERM cancel ctx, which terminates
	// Server.Run.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.Run(ctx, opts.Transport); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("thor-mcp stopped (context cancelled)")
			return nil
		}
		return fmt.Errorf("mcp run: %w", err)
	}
	logger.Info("thor-mcp stopped")
	return nil
}

// newServer builds the *mcp.Server with our advertised identity.
// Kept separate so tests can construct it without invoking Run.
func newServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{
		Name:    ServerName,
		Version: version.Short(),
	}, nil)
}
