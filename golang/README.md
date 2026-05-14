# Thor (Go port)

Production-grade Go rewrite of the Thor PSA Validator. Single static binary
per platform, distributed to AWS partners. The Python implementation in
[`../server/`](../server) remains the reference until parity is reached —
see [PLAN.md](PLAN.md) for the full roadmap.

This README will grow alongside the implementation. Today it covers the
build/test loop a contributor needs to get started.

## Status

Phase 2 — `thor` CLI surface is complete. `thor-mcp` is still a stub
(Phase 3). Track progress in [PLAN.md](PLAN.md).

## Requirements

- Go 1.23 or newer (`go version`)
- GNU Make
- (CI/lint) [golangci-lint](https://golangci-lint.run/welcome/install/) v2

## Build

```sh
cd golang
make build               # builds bin/thor and bin/thor-mcp
./bin/thor --version
```

## Install locally (for dogfooding)

```sh
make install-local       # copies binaries to ~/.local/bin (override with THOR_INSTALL_DIR)
make uninstall-local
```

The first run prints whether `THOR_INSTALL_DIR` is on `PATH` and what to add
to your shell rc if not.

## Development loop

```sh
make fmt                 # gofmt + goimports
make vet                 # go vet ./...
make test                # go test ./...
make lint                # golangci-lint run ./...
make verify-prompts      # fail if golang/prompts/ has drifted from server/thor/tools/
make sync-prompts        # copy prompts from the Python tree into golang/prompts/
```

## CLI reference

`thor --help` lists every command. Exit codes:

| Code | Meaning |
|:----:|---------|
| 0 | success |
| 1 | generic error |
| 2 | invalid usage (unknown flag/arg) |
| 3 | AWS credentials expired (run `aws sso login --profile …` or `aws configure`) |
| 4 | partial failure — some controls errored; verdict-level FAILs are *not* this case |

### Global flags

Every subcommand accepts:

- `--region` (env: `AWS_REGION`, `AWS_DEFAULT_REGION`) — Bedrock region.
- `--profile` (env: `AWS_PROFILE`) — named AWS profile.
- `--verbose` / `-v` (env: `THOR_VERBOSE`) — verbose log output.
- `--json` (env: `THOR_JSON`) — machine-readable JSON output where applicable.

### Commands

```sh
# Health check: AWS creds, Bedrock model access, embedded prompts.
thor doctor [--json]

# Excel → partner_responses.csv.
thor convert <partner-folder> [--app-type SERVICE|SOFTWARE]

# Build the control→files evidence map (auto-built by `thor validate`).
thor map <partner-folder> [--concurrency N] [--model-id <id>]

# Run the validator. By default, builds/loads evidence_map.json so each
# control sees only its mapped files. Pass --no-map to skip that pre-pass.
thor validate <partner-folder> \
    [--controls "ACCT-001 COST-001"] \
    [--category "Generative AI Applications"] \
    [--consensus N] [--concurrency N] \
    [--skip-conversion] \
    [--no-map] \
    [--system-mode old|new|revised] \
    [--app-type SERVICE|SOFTWARE]

# Compare validation runs over time.
thor diff <partner-folder> [--mode latest|all|custom] [--run1 <ts>] [--run2 <ts>]

# Create a partner folder skeleton ready for evidence.
thor prep <partner-name> --category <name> [--app-type SERVICE|SOFTWARE] [--folder <path>]

# List the controls Thor knows about (optionally filtered by category).
thor list-controls [--category <name>]

# Render a validation_summary.md to a self-contained HTML file.
thor export <partner-folder> [--report <ts>]

thor --version
```

### Typical workflow

```sh
# 1. Stage partner evidence
thor prep Acme --category "Generative AI Applications"
# (drop the partner Excel into ~/Documents/PSA_Validations/Acme/ and
#  evidence files into Acme/supporting_docs/)

# 2. Convert, then validate. The first run builds evidence_map.json
#    (one Bedrock call per supporting file with the full CONTEXT.csv
#    catalog) so each control sees only relevant evidence. Subsequent
#    runs reuse the map until supporting_docs/ contents change.
thor convert ~/Documents/PSA_Validations/Acme
thor validate ~/Documents/PSA_Validations/Acme

# 3. Inspect / share
thor export ~/Documents/PSA_Validations/Acme
thor diff   ~/Documents/PSA_Validations/Acme   # after a second run
```

## Layout

```
golang/
├── cmd/
│   ├── thor/             # CLI entrypoint (Phase 2)
│   └── thor-mcp/         # MCP stdio server (Phase 3)
├── internal/
│   ├── awsauth/          # SDK config + credential diagnostics
│   ├── cli/              # cobra command tree (consumed by cmd/thor)
│   ├── controls/         # CONTEXT.csv loader + category mapping
│   ├── evidence/         # control→files mapping pre-pass + persistence
│   ├── excel/            # .xlsx → partner_responses.csv
│   ├── mcpserver/        # MCP server wiring (consumed by cmd/thor-mcp)
│   ├── pdf/              # pdfcpu split + text fallback
│   ├── prompts/          # //go:embed for system_*.txt + CONTEXT.csv
│   ├── report/           # Markdown + HTML + diff/timeline rendering
│   ├── runlog/           # slog + run manifest writer
│   ├── validator/        # Bedrock orchestration
│   └── version/          # ldflags-injected build metadata
├── claude-skill/         # Claude Code integration (template — copy into your project)
├── kiro-power/           # Kiro Power (template — install from this dir)
├── CLAUDE.md             # symlink → claude-skill/CLAUDE.md (auto-loaded in golang/)
├── .mcp.json             # symlink → claude-skill/mcp.json (auto-loaded in golang/)
├── .claude/skills/       # symlinks → claude-skill/skills/ (auto-loads when working in golang/)
├── .kiro/steering/       # symlinks → kiro-power/steering/ (auto-loads when working in golang/)
├── scripts/              # sync-prompts.sh, verify-prompts.sh
└── .github/workflows/    # ci.yml (test/lint/build on PR)
```

## Choose your install

Two implementations live in this repo:

- **Python (`../server/`, root `claude-skill/`, root `POWER.md`)** — the
  original reference behaviour, requires a venv. Stays at the repo root
  indefinitely.
- **Go (`./`, `./claude-skill/`, `./kiro-power/`)** — single binary, no
  runtime dependencies, planned default once parity ships.

You can install both side-by-side: the Go Kiro Power registers as
`thor-psa-validator-go` so it doesn't collide with the Python
`thor-psa-validator`. The Go skill/power live entirely under `golang/`
and never touch the Python skill/power at the repo root.

### Claude Code

`golang/claude-skill/` is a copy-friendly template: copy `mcp.json` to
your project's `.mcp.json`, optionally copy `CLAUDE.md` and the
`skills/` subtree, then run `thor doctor`. See
[`claude-skill/README.md`](claude-skill/README.md) for the step-by-step.

When iterating on Thor itself, just open the `golang/` directory in
Claude Code — `golang/.claude/skills/thor-validate.md` is symlinked to
the canonical file in `claude-skill/`, so the skill auto-loads with no
copy step.

### Kiro

`golang/kiro-power/` is a self-contained Kiro Power. Install it from
the Kiro Powers panel pointed at this directory, then run `thor_doctor`
in Kiro to verify. See [`kiro-power/README.md`](kiro-power/README.md)
for details.

When iterating on Thor itself, open the `golang/` directory in Kiro —
`golang/.kiro/steering/` is symlinked to the canonical files in
`kiro-power/steering/`, so steering auto-loads with no install step.
