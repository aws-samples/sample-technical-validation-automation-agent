# Thor PSA Validator

AI-powered partner competency validation tool. Validates AWS partner
submissions against the PSA control catalog using Amazon Bedrock (Claude)
and produces structured PASS / FAIL / WAIVED reports.

Ships as a single static binary — no Python, no runtime dependencies.

## Quick Start

```sh
# Build
make build

# Install to ~/.local/bin
make install-local

# Verify
thor doctor
```

## Using with Kiro

Install this repo as a Kiro Power from the Powers panel. See
[`POWER.md`](POWER.md) for full setup instructions.

## Using with Claude Code

See [`claude-skill/README.md`](claude-skill/README.md) for setup.

## Repo Structure

```
thor-power/
├── cmd/
│   ├── thor/              # CLI entrypoint
│   └── thor-mcp/          # MCP stdio server
├── internal/              # Shared engine (validator, excel, pdf, etc.)
├── claude-skill/          # Claude Code template
├── steering/              # Kiro steering files
├── scripts/               # Prompt sync/verify scripts
├── POWER.md               # Kiro Power manifest
├── mcp.json               # Kiro MCP server config
├── SPEC.md                # Behavioural specification
├── PLAN.md                # Remaining roadmap
├── Makefile               # Build targets
└── go.mod                 # Go module
```

## Available Tools

| Tool | Description |
|------|-------------|
| `thor_doctor` | Health check — embedded prompts, AWS credentials, Bedrock access |
| `thor_convert` | Convert Excel checklist (.xlsx) to CSV |
| `thor_map` | Build the control→files evidence map |
| `thor_validate` | Run Bedrock AI validation (parallel, with fallback) |
| `thor_diff` | Compare validation runs |
| `thor_prep` | Create a new partner folder |
| `thor_list_controls` | List available controls |
| `thor_export` | Export report to HTML |

## Development

```sh
make fmt          # format
make vet          # go vet
make test         # unit tests
make lint         # golangci-lint
make tidy         # go mod tidy
```

## Credential Handling

Uses the AWS SDK default credential chain. If credentials expire
mid-run, refresh and retry — no restart needed.
