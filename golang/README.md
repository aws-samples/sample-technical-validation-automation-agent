# Thor PSA Validator

Thor validates AWS Partner Solution Assessment (PSA) submissions against the
program's control catalog using Amazon Bedrock (Anthropic Claude). It ships
as a single static binary plus a paired MCP server, so partners (and the
agents that drive them) can run a complete validation from a partner-folder
on disk to an auditable HTML report.

```
PartnerName/                   ┌──────────────┐
├── checklist.xlsx       ─────►│ thor convert │──► partner_responses.csv
├── partner_responses.csv      └──────────────┘
├── supporting_docs/                  │
│   ├── architecture.pdf              ▼
│   └── security_policy.pdf  ┌────────────────┐
│                            │   thor map     │──► evidence_map.json
│                            └────────────────┘
│                                     │
│                                     ▼
│                            ┌────────────────┐
│                            │ thor validate  │──► validation_summary.md
│                            └────────────────┘    reports/run_<ts>.json
│                                     │            reports/summary/…
│                                     ▼
│                            ┌────────────────┐
│                            │  thor export   │──► validation_report_*.html
│                            └────────────────┘
```

- **`thor`** — CLI for humans and shell scripts.
- **`thor-mcp`** — MCP stdio server that exposes the same operations to
  Claude Code, Kiro, and any other MCP-aware agent.

Both binaries are produced from this repo. They share a single engine in
`internal/`, embed every prompt and the control catalog
(`CONTEXT.csv`) directly into the binary (no runtime files, no
`--prompts-dir`), and resolve AWS credentials through the standard SDK
default chain.

## Requirements

- Go 1.23 or newer (`go version`).
- GNU Make.
- AWS credentials with Bedrock access in a region where the
  `global.anthropic.claude-sonnet-4-5` inference profile is available.
- For lint runs: [golangci-lint](https://golangci-lint.run/welcome/install/) v2.

There are no other external dependencies — long PDFs are split with the
in-process `pdfcpu` library, and `.pptx` / `.docx` text extraction uses
only the Go standard library.

## Build

```sh
cd golang
make build               # builds bin/thor and bin/thor-mcp
./bin/thor --version
```

## Install locally

```sh
make install-local       # copies binaries to ~/.local/bin
                         # override with THOR_INSTALL_DIR=...
make uninstall-local
```

The first run prints whether `THOR_INSTALL_DIR` is on `PATH` and exactly
what to add to your shell rc if not.

## Development loop

```sh
make fmt                 # gofmt + goimports
make vet                 # go vet ./...
make test                # go test ./...
make test-cover          # go test -coverprofile=coverage.out
make lint                # golangci-lint run ./...
make tidy                # go mod tidy
make sync-prompts        # refresh embedded prompts from canonical sources
make verify-prompts      # CI gate — fails if embedded prompts have drifted
```

## Concepts

- **Designation category.** Filters which controls apply — for example
  *Generative AI Applications* excludes FM-training-specific controls.
  The set lives in `internal/controls`.
- **Application type.** `SERVICE` or `SOFTWARE`. Drives the suffix
  applied to certain control IDs (e.g. `DOC-001-SERVICE` vs
  `DOC-001-SOFTWARE`).
- **Evidence map.** A control→files mapping built by a Bedrock
  pre-pass. `thor validate` builds it automatically the first time a
  partner folder is validated and reuses it on subsequent runs
  (regenerated when files under `supporting_docs/` change). The map keeps
  each per-control Bedrock call under Bedrock's 5-document-block /
  100-page-per-request limits and dramatically reduces noise. Run
  `thor map` explicitly to inspect or regenerate it; pass `--no-map`
  to fall back to "send every file to every control".
- **Consensus mode.** Runs each control N times and majority-votes the
  verdicts for borderline cases. When `consensus = 3` and every control
  passed runs 1 and 2, run 3 is skipped.
- **Run manifest.** Every validation writes
  `<partner>/reports/run_<ts>.json` — an auditable record of the run ID,
  start/finish times, model ID, prompt version, partner-folder hash, and
  per-control verdict + reasoning.

## CLI reference

`thor --help` lists every command. Every subcommand accepts the global
flags below.

### Global flags

| Flag | Env | Description |
|---|---|---|
| `--region` | `AWS_REGION`, `AWS_DEFAULT_REGION` | Bedrock region. |
| `--profile` | `AWS_PROFILE` | Named AWS profile. |
| `--verbose` / `-v` | `THOR_VERBOSE` | Verbose log output to stderr. |
| `--json` | `THOR_JSON` | Emit machine-readable JSON where applicable (currently `thor doctor`). |
| `--version` | — | Print version + commit + Go runtime and exit. |

### Exit codes

| Code | Meaning |
|:----:|---------|
| 0 | success |
| 1 | generic error |
| 2 | invalid usage (unknown flag/arg) |
| 3 | AWS credentials expired (refresh and retry — no restart needed) |
| 4 | partial failure — some controls ERRORED. Verdict-level FAILs are *not* this case |

### Commands

```sh
# Health check: AWS creds, Bedrock model access, embedded prompts.
thor doctor [--json]

# Excel → partner_responses.csv (auto-picks the most recently
# modified .xlsx in the folder).
thor convert <partner-folder> [--app-type SERVICE|SOFTWARE]

# Build the control→files evidence map (also auto-built by `thor validate`).
thor map <partner-folder>
    [--concurrency N] [--model-id <id>] [--app-type SERVICE|SOFTWARE]

# Run the validator. By default, builds/loads evidence_map.json so each
# control sees only its mapped files. Pass --no-map to skip that pre-pass.
thor validate <partner-folder>
    [--controls "ACCT-001 COST-001"]
    [--category "Generative AI Applications"]
    [--consensus N] [--concurrency N]
    [--skip-conversion] [--no-map]
    [--system-mode old|new|revised]
    [--app-type SERVICE|SOFTWARE]

# Compare two validation runs.
thor diff <partner-folder>
    [--mode latest|all|custom]
    [--run1 <ts>] [--run2 <ts>]

# Create a partner folder skeleton ready for evidence.
thor prep <partner-name> --category <name>
    [--app-type SERVICE|SOFTWARE]
    [--folder <path>]

# List the controls Thor knows about (optionally filtered by category).
thor list-controls [--category <name>]

# Render a validation_summary.md to a self-contained HTML file.
thor export <partner-folder> [--report <ts>]
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
#    runs reuse the map until supporting_docs/ changes.
thor convert ~/Documents/PSA_Validations/Acme
thor validate ~/Documents/PSA_Validations/Acme

# 3. Inspect / share
thor export ~/Documents/PSA_Validations/Acme
thor diff   ~/Documents/PSA_Validations/Acme   # after a second run
```

## Partner folder layout

```
PartnerName/
├── checklist.xlsx              # Excel checklist (root level)
├── partner_responses.csv       # Generated by thor convert
├── evidence_map.json           # Generated by thor map (or auto by thor validate)
├── supporting_docs/            # PDFs, architecture diagrams, screenshots
├── validation_summary.md       # Latest validation results
├── validation_progress.log     # Live progress during validation
└── reports/
    ├── run_<ts>.json           # Auditable per-run manifest
    └── summary/                # Timestamped archived markdown reports
```

`thor convert` always emits the canonical `controlId,partner_response`
header; CSVs with the older `control_id` column are accepted on read and
canonicalized to `controlId` in memory.

## MCP server

`thor-mcp` is the same engine wrapped in a stdio MCP server. Tools
exposed:

| Tool | Description |
|---|---|
| `thor_doctor` | Health check (embedded prompts, AWS credentials, Bedrock access). |
| `thor_convert` | Convert the partner Excel checklist to `partner_responses.csv`. |
| `thor_map` | Build the control→files evidence map (auto-built by `thor_validate` when missing or stale). |
| `thor_validate` | Run the validator end-to-end (parallel, all applicable controls). |
| `thor_diff` | Compare two validation runs (latest, custom timestamps, or full timeline). |
| `thor_prep` | Create a new partner folder with the right structure. |
| `thor_list_controls` | List available controls, optionally filtered by category. |
| `thor_export` | Render `validation_summary.md` to a self-contained HTML file. |

The server writes operator logs as JSON to stderr only — `os.Stdout` is
reserved for the MCP transport. Concurrent tool calls are safe; the
underlying `Validator` is goroutine-safe and uses the same retry /
concurrency budget for every call.

### Wiring an agent

`golang/claude-skill/` is a copy-friendly Claude Code template (see
[`claude-skill/README.md`](claude-skill/README.md) for the
step-by-step), and `golang/kiro-power/` is a self-contained Kiro Power
(see [`kiro-power/README.md`](kiro-power/README.md)).

When iterating on Thor itself, just open the `golang/` directory in your
agent of choice — the runtime symlinks under `golang/.claude/` and
`golang/.kiro/` already point at the canonical files in `claude-skill/`
and `kiro-power/`, so the skill / steering / MCP wiring auto-loads with
no copy step.

## Layout

```
golang/
├── cmd/
│   ├── thor/                  # CLI entrypoint
│   └── thor-mcp/              # MCP stdio server
├── internal/
│   ├── awsauth/               # SDK config + credential diagnostics
│   ├── cli/                   # cobra command tree
│   ├── controls/              # CONTEXT.csv loader + category mapping
│   ├── evidence/              # control→files mapper + persistence
│   ├── excel/                 # .xlsx → partner_responses.csv
│   ├── mcpserver/             # MCP server wiring
│   ├── pdf/                   # pdfcpu split + text fallback
│   ├── prompts/               # //go:embed for system_*.txt + CONTEXT.csv
│   ├── report/                # Markdown + HTML + diff/timeline rendering
│   ├── runlog/                # slog + run manifest writer
│   ├── validator/             # Bedrock orchestration
│   └── version/               # ldflags-injected build metadata
├── claude-skill/              # Claude Code template
├── kiro-power/                # Kiro Power template
├── scripts/                   # sync-prompts.sh, verify-prompts.sh
├── PLAN.md                    # remaining roadmap (release pipeline + hardening)
├── SPEC.md                    # behavioural specification
└── README.md
```

## Credential refresh

If a Bedrock call returns an expired-credentials error, refresh and
retry — no restart needed. Pick whichever applies:

```
aws sso login --profile <profile>     # SSO users
aws configure                         # static keys
isengardcli assume <account>          # Amazon Cloud Desktop
ada credentials update                # alternative for Cloud Desktop
```

`thor doctor` and any failed Bedrock call print the same combined hint
on stderr.

## Further reading

- [`SPEC.md`](SPEC.md) — full behavioural specification: command flags,
  MCP tool schemas, file formats, exit codes, env vars.
- [`PLAN.md`](PLAN.md) — remaining roadmap (release pipeline,
  observability, hardening).
- [`claude-skill/README.md`](claude-skill/README.md) — Claude Code
  setup.
- [`kiro-power/README.md`](kiro-power/README.md) — Kiro setup.
