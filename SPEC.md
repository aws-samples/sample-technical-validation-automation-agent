# Thor PSA Validator — Specification

This is the canonical behavioural specification for Thor. It describes
what the binary does, the file formats it reads and writes, the
contracts of its CLI and MCP surfaces, and the exact defaults the
engine ships with. Implementation lives in `internal/`; `README.md`
covers the user-facing install and quickstart; `PLAN.md` covers what
remains to ship.

---

## 1. System overview

Thor is a single static Go binary distributed in two flavours:

| Binary | Purpose | Transport |
|---|---|---|
| `thor` | Interactive CLI for humans and shell pipelines. | stdin/stdout/stderr. |
| `thor-mcp` | MCP stdio server exposing the same engine to AI agents. | MCP over stdio (JSON-RPC). |

Both binaries link the same engine in `internal/`. They embed the
system prompts and the control catalog (`CONTEXT.csv`) directly via
`//go:embed` — there are no runtime files, no `--prompts-dir`, no
configuration files. Behaviour is configured exclusively through CLI
flags, MCP tool arguments, and a small set of environment variables.

```
                         ┌──────────────┐                ┌────────────┐
                         │  cmd/thor    │ flags+args     │ internal/  │
                         │  (cobra CLI) ├────────────────┤   cli      │
                         └──────────────┘                ├────────────┤
                                                         │ controls   │
                         ┌──────────────┐ MCP tools      │ excel      │
                         │ cmd/thor-mcp ├────────────────┤ pdf        │
                         │ (MCP server) │                │ evidence   │
                         └──────────────┘                │ validator  │
                                                         │ awsauth    │
                                                         │ report     │
                                                         │ runlog     │
                                                         │ prompts    │
                                                         └─────┬──────┘
                                                               │
                                                       Bedrock Converse
                                                  (Anthropic Claude Sonnet 4.5)
```

## 2. Module layout

```
golang/
├── cmd/
│   ├── thor/                    # CLI entrypoint
│   └── thor-mcp/                # MCP stdio server entrypoint
├── internal/
│   ├── awsauth/                 # SDK config + credential diagnostics
│   ├── cli/                     # cobra command tree
│   ├── controls/                # CONTEXT.csv loader + category mapping + suffix list
│   ├── evidence/                # control→files mapper + persistence
│   ├── excel/                   # .xlsx → partner_responses.csv
│   ├── mcpserver/               # MCP server wiring (consumed by cmd/thor-mcp)
│   ├── pdf/                     # pdfcpu split + ledongthuc/pdf text fallback
│   ├── prompts/                 # //go:embed for system_*.txt + CONTEXT.csv
│   ├── report/                  # Markdown + HTML + diff/timeline rendering
│   ├── runlog/                  # slog logger + run manifest writer
│   ├── validator/               # Bedrock orchestration + result parsing
│   └── version/                 # ldflags-injected build metadata
├── claude-skill/                # Claude Code template
└── kiro-power/                  # Kiro Power template
```

Module path: `thor-golang`. Distribution is via release tarballs (see
`PLAN.md` Phase 4), not `go install`.

## 3. Command specification (`thor`)

### 3.1 Global flags

Every subcommand accepts these. Both flags and environment variables
are honoured; flags take precedence.

| Flag | Env | Type | Default | Description |
|---|---|---|---|---|
| `--region` | `AWS_REGION`, `AWS_DEFAULT_REGION` | string | "" | Bedrock region. |
| `--profile` | `AWS_PROFILE` | string | "" | Named AWS profile. |
| `--verbose`, `-v` | `THOR_VERBOSE` | bool | false | Verbose stderr logging. |
| `--json` | `THOR_JSON` | bool | false | JSON output where applicable (currently `thor doctor`). |
| `--version` | — | — | — | Print `version commit (date)` and exit. |
| `--help`, `-h` | — | — | — | Help text for the command. |

`THOR_VERBOSE` and `THOR_JSON` accept `1` / `true` / `yes` / `on` (any
other non-empty value is treated as true; `0` / `false` / `no` / `off`
/ empty as false).

### 3.2 Exit codes

| Code | Meaning |
|:----:|---------|
| 0 | Success. |
| 1 | Generic error. |
| 2 | Invalid usage (unknown flag/arg, malformed value). |
| 3 | AWS credentials expired. |
| 4 | Partial failure — at least one control ERRORED. Verdict-level FAILs are *not* this case (those exit 0). |

### 3.3 Subcommands

#### `thor doctor [--json]`

Health check. Verifies:

1. The four embedded prompts (`system_old.txt`, `system_new.txt`,
   `system_revised.txt`, `CONTEXT.csv`) load.
2. `awsauth.LoadConfig` resolves credentials and STS
   `GetCallerIdentity` succeeds.
3. The active region exposes at least one inference profile whose ID
   starts with `global.anthropic.claude-sonnet-4-5`.

Exits 0 on success, 3 on expired credentials, 1 on any other failure.
With `--json`, emits:

```json
{
  "embeddedPrompts": {
    "OK": true, "HasOld": true, "HasNew": true,
    "HasRevised": true, "HasContext": true,
    "ContextRows": <int>
  },
  "aws": {
    "Region": "...", "Profile": "...",
    "CredentialSource": "...",
    "Identity": { "Account": "...", "ARN": "..." },
    "Bedrock": {
      "InferenceProfileCount": <int>,
      "HasModelMatch": <bool>
    }
  },
  "errors": ["..."]
}
```

#### `thor convert <partner-folder> [--app-type SERVICE|SOFTWARE]`

Reads the most-recently-modified `.xlsx` / `.xls` in `<partner-folder>`
(skipping Office tilde locks `~$*`) and writes
`<partner-folder>/partner_responses.csv` with header
`controlId,partner_response`.

`--app-type` (default `SOFTWARE`) drives the suffix applied to certain
control IDs (see `internal/controls.SuffixControls`).

#### `thor map <partner-folder> [--concurrency N] [--model-id <id>] [--app-type SERVICE|SOFTWARE]`

Builds the control→files evidence map and persists it to
`<partner-folder>/evidence_map.json`. Auto-runs `thor convert` first
if `partner_responses.csv` is missing. The mapper is
declared-driven: it only assigns files to controls the partner
declared in their checklist.

Defaults:
- `--concurrency`: `evidence.DefaultConcurrency` (6).
- `--model-id`: `evidence.DefaultModelID` (`global.anthropic.claude-sonnet-4-5-20250929-v1:0`).
- `--app-type`: `SOFTWARE`.

#### `thor validate <partner-folder> [flags...]`

Runs the validator. Default behaviour:

1. Run `thor convert` unless `--skip-conversion` is set.
2. Load `partner_responses.csv` and intersect with `CONTEXT.csv`.
3. Either ensure a fresh `evidence_map.json` exists (default), or skip
   the mapping pre-pass if `--no-map` is set.
4. For each applicable control, send one Bedrock Converse call with
   the control's prompt context, the partner's response, and the
   filtered evidence files (via the map). Marketplace control
   `DOC-006` short-circuits Bedrock and runs an HTTP probe instead.
5. Write results.

Flags:

| Flag | Default | Description |
|---|---|---|
| `--controls "ID1 ID2"` | "" | Space-separated control IDs to run. Overrides `--category`. |
| `--category <name>` | "" | Designation category to filter applicable controls. |
| `--consensus N` | 1 | Run each control N times and majority-vote. |
| `--concurrency N` | 10 (`validator.DefaultConcurrency`) | Max in-flight Bedrock calls. |
| `--skip-conversion` | false | Reuse existing `partner_responses.csv`. |
| `--no-map` | false | Skip evidence map; send every supporting file to every control. |
| `--system-mode old\|new\|revised` | `revised` | Which embedded system prompt to use. |
| `--app-type SERVICE\|SOFTWARE` | `SOFTWARE` | Used during conversion. |

Outputs (see §6 for formats):
- `<folder>/validation_summary.md` (root copy, overwritten each run)
- `<folder>/reports/summary/validation_summary_<UTC ts>.md` (timestamped)
- `<folder>/reports/run_<UTC ts>.json` (auditable run manifest)
- Per-control progress lines on stderr.

Exit code 4 if any control ERRORED; otherwise 0 (regardless of
PASSED/FAILED/WAIVED counts).

#### `thor diff <partner-folder> [--mode latest|all|custom] [--run1 <ts>] [--run2 <ts>]`

Compares saved validation runs from `reports/summary/`. Modes:

- `latest` (default): diff the two most recent timestamped reports.
- `all`: emit a markdown timeline table with per-control verdicts
  across every saved run.
- `custom`: pick two runs by timestamp substring (`--run1`, `--run2`).
  Empty `--run1` means "earliest"; empty `--run2` means "latest".

Errors with exit 1 if fewer than 2 reports exist for any non-`all`
mode.

#### `thor prep <partner-name> --category <name> [--app-type SERVICE|SOFTWARE] [--folder <path>]`

Creates a partner folder skeleton:

```
<base>/<partner-name>/
├── supporting_docs/
└── reports/
    └── summary/
```

`<base>` defaults to `~/Documents/PSA_Validations`. Prints next-step
guidance (`thor convert`, `thor validate`).

`--category` is required; the command counts and prints how many
controls are applicable.

#### `thor list-controls [--category <name>]`

Lists every control in the embedded `CONTEXT.csv`. With `--category`,
filters via `controls.GetApplicable`. Output is plain text, one
controlId per line, with a header counting the matches.

#### `thor export <partner-folder> [--report <ts>]`

Renders a markdown summary to a self-contained HTML file at
`<partner-folder>/validation_report_<partner-name>.html`. Without
`--report`, picks (in order) the root `validation_summary.md` or the
most recent timestamped report. With `--report`, matches a timestamp
substring inside `reports/summary/`.

## 4. MCP specification (`thor-mcp`)

### 4.1 Server identity

- `serverInfo.name`: `"thor-psa-validator"`.
- `serverInfo.version`: from `internal/version` (matches `thor --version`).
- Transport: stdio. Operator logs go to stderr only; `os.Stdout` is
  reserved for the MCP transport.

### 4.2 Tools

Eight tools, one-to-one with the CLI commands above. Input schemas
are auto-generated by the MCP SDK from the typed structs in
`internal/mcpserver/tools.go`.

| Tool | Input fields | Behaviour |
|---|---|---|
| `thor_doctor` | _(none)_ | Returns a JSON-formatted `DiagnosticReport`. |
| `thor_convert` | `partner_folder` (required), `app_type` | Writes `partner_responses.csv`. Returns `Extracted N response(s) from <xlsx> -> <csv>`. |
| `thor_map` | `partner_folder` (required), `concurrency`, `model_id`, `app_type` | Writes `evidence_map.json`. Returns a one-line summary. |
| `thor_validate` | `partner_folder` (required), `controls`, `category`, `app_type`, `consensus`, `concurrency`, `skip_conversion`, `no_map`, `system_mode` | Writes summary + manifest. Returns `Validated N control(s): passed=… failed=… waived=… errored=…`. |
| `thor_diff` | `partner_folder` (required), `mode`, `run1`, `run2` | Returns the rendered diff or timeline as text. |
| `thor_prep` | `partner_name` (required), `category` (required), `app_type`, `folder` | Creates partner-folder skeleton. Returns a one-line summary. |
| `thor_list_controls` | `category` | Returns a markdown list of control IDs. |
| `thor_export` | `partner_folder` (required), `report` | Writes the HTML file. Returns `Exported HTML to <path>`. |

Errors are returned as `CallToolResult{IsError: true,
Content: [TextContent{Text: <error.Error()>}]}`. Successes return
`CallToolResult{Content: [TextContent{Text: <output>}]}`.

### 4.3 Concurrency and shutdown

- `Validator` is goroutine-safe; the MCP server can serve multiple
  tool calls concurrently. Each call gets its own
  `bedrockruntime.Client`-backed Validator instance via the wired
  factory.
- `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` cancels
  the server context for graceful shutdown.
- Operator logs use `runlog.NewLogger`, which writes JSON lines to
  stderr only.

## 5. Validator behaviour

### 5.1 Inputs

For each control, the validator assembles:

1. The embedded system prompt (`system_old.txt`, `system_new.txt`, or
   `system_revised.txt` — selectable via `system_mode`).
2. The user message:
   - The control's `prompt_context` text from `CONTEXT.csv`.
   - The partner response: `Partner Response from self-assessment:\nResponse for <controlId> submitted by Partner: <text>`.
   - The evidence content blocks (see §5.3).
   - The closing question: `Based on the details provided, is this offering approved?`
3. Inference config: `MaxTokens=2000`, `Temperature=0.0`.

Bedrock model: `global.anthropic.claude-sonnet-4-5-20250929-v1:0`
(`validator.DefaultModelID`). SDK retry: `MaxAttempts=6`. Per-call
deadline: 120 s (`validator.DefaultPerCallTimeout`).

### 5.2 Result parsing

Bedrock's free-form response is parsed once, in
`validator.parseResult`. The parser tests for these prefixes (anchored
on the first non-whitespace token, case-sensitive):

- `YES.` → `StatusPassed`
- `NO.` → `StatusFailed`
- `WAIVED.` → `StatusWaived`
- otherwise → `StatusFailed` (the prefix-less text becomes the
  reasoning).

Errors during the call surface as `Result{Status: StatusErrored, Err: ...,
Reasoning: <actionable text>}`. Reasoning is never empty, even on
error paths.

### 5.3 Evidence routing

`internal/validator/files.go` routes each path under `supporting_docs/`
into Bedrock content blocks. Routing rules:

| Extension | Block type | Notes |
|---|---|---|
| `.pdf, .xlsx, .xls, .docx, .doc, .csv, .html, .txt, .md` | `document` (raw bytes) | Sent natively; oversized files (>4.5 MB) fall back to text extraction. |
| `.png, .jpg, .jpeg` | `image` | Resized to ≤8000 px on the long side via `golang.org/x/image/draw.CatmullRom`. |
| `.pptx` | `document` (`txt`) | Always text-extracted via stdlib `archive/zip` + `encoding/xml`, regardless of size. |
| Any unknown extension | _(skipped)_ | Logged at info level. |

Per-call Bedrock caps the validator enforces:

- **`MaxPDFPagesPerCall = 95`**: aggregate PDF page count across all
  document blocks. When exceeded, the largest PDFs are demoted to
  text (`pdf.ExtractText`) until the budget fits. Page counting uses
  `pdfcpu`.
- **`MaxDocBlocksPerCall = 5`**: hard ceiling. The validator keeps the
  top-ranked 4 documents as full-fidelity binary blocks
  (`MaxBinaryDocBlocks`) and merges the rest into a single appendix
  text block (`textDocBlock("appendix", ...)`).
- Image blocks don't count toward the 5-document cap.

### 5.4 DOC-006 marketplace check

`DOC-006` short-circuits Bedrock and uses
`validator.MarketplaceChecker` (HTTP-based) instead. Behaviour:

- Empty partner response → `StatusWaived`, reasoning `no partner response provided — manual review required`.
- N/A response (response <50 chars after trim and equals one of `n/a`,
  `not applicable`, `not available`, case-insensitive) →
  `StatusWaived`.
- Otherwise: extract Marketplace URLs matching
  `https?://(?:www\.)?aws\.amazon\.com/marketplace/pp/[^\s,)"']+`,
  HEAD-probe each one with a 10 s timeout. Redirects only follow when
  the destination host is `aws.amazon.com`. Any 4xx fails the check.

### 5.5 Concurrency

`ValidateBatch` schedules per-control work with
`golang.org/x/sync/errgroup` and `golang.org/x/sync/semaphore`. Default
concurrency is 10; `--concurrency` / the `concurrency` MCP arg
overrides it.

### 5.6 Consensus mode

When `consensus > 1`:

- Run N rounds of `runOnce`, each producing a per-control map.
- Reduce by majority vote: `passed > failed` → `StatusPassed`;
  otherwise the first non-pass result wins.
- **Early-stop** triggers only when `consensus == 3` AND every control
  passed in runs 1 and 2 (ERRORED controls do not satisfy "passed").
  Skip run 3.
- Final reasoning is annotated `Consensus N/T. <original>` or
  `Consensus N/T not passing. <original>`.

## 6. File formats

### 6.1 `partner_responses.csv`

UTF-8, RFC 4180. Header is always the canonical:

```
controlId,partner_response
```

Legacy CSVs with header `control_id,partner_response` are accepted on
read and canonicalized to `controlId` in memory. `controlId` values
include the suffix where applicable (`DOC-001-SOFTWARE`,
`DOC-001-SERVICE`).

### 6.2 `evidence_map.json`

Persisted at `<partner-folder>/evidence_map.json`. Schema:

```json
{
  "schemaVersion": 2,
  "generatedAt": "2026-05-14T00:00:00Z",
  "partnerFolderHash": "<16-hex-chars>",
  "modelId": "global.anthropic.claude-sonnet-4-5-20250929-v1:0",
  "thorVersion": "v0.1.0-rc1",
  "declaredControls": ["ACCT-001", "DOC-001-SOFTWARE", "..."],
  "controls": {
    "ACCT-001": ["security_policy.pdf", "iam_diagram.pdf"],
    "DOC-001-SOFTWARE": ["architecture.pdf"]
  },
  "files": {
    "security_policy.pdf": ["ACCT-001"],
    "architecture.pdf": ["DOC-001-SOFTWARE"]
  },
  "unmapped": ["unrelated_invoice.pdf"],
  "emptyControls": ["DOC-006"]
}
```

- `controls[id]` is rank-ordered (highest mapper-confidence first).
- `partnerFolderHash` is the first 16 hex chars of a SHA-256 of
  `partner_responses.csv` plus every file under `supporting_docs/`
  (sorted by relative path; size + bytes mixed in). `thor validate`
  invalidates the map when the hash changes or `declaredControls`
  drifts.
- `unmapped` lists supporting files that the mapper couldn't tie to
  any declared control (surfaced as warnings; review manually).
- `emptyControls` lists declared controls that, after both mapper
  passes, have zero attached files. The validator treats these as
  "send no evidence" — partner response only.

### 6.3 `validation_summary.md`

Markdown body produced by `report.RenderMarkdown`. Contains a
heading, optional metadata (partner name, timestamp), and one section
per control showing the Verdict (`YES.` / `NO.` / `WAIVED.` /
`ERRORED.`) followed by the trimmed reasoning text.

### 6.4 `reports/run_<ts>.json`

Auditable per-run manifest produced by `runlog.WriteManifest`:

```json
{
  "id": "<UUID v4>",
  "startedAt": "2026-05-14T01:23:45Z",
  "completedAt": "2026-05-14T01:28:10Z",
  "thorVersion": "v0.1.0-rc1",
  "modelId": "global.anthropic.claude-sonnet-4-5-20250929-v1:0",
  "promptVersion": "revised",
  "partnerFolderHash": "<16-hex-chars>",
  "controls": ["ACCT-001", "..."],
  "results": [
    {
      "controlId": "ACCT-001",
      "status": "PASSED",
      "verdict": "YES",
      "reasoning": "...",
      "error": ""
    }
  ]
}
```

Filename uses the UTC start timestamp:
`run_YYYYMMDD_HHMMSS.json`.

### 6.5 `validation_progress.log`

Free-form per-control progress log. The same lines are written to
stderr. Format is governed by `runlog.NewProgressLogger` —
JSON-structured `slog` records, one per control event, stamped with
the run ID.

## 7. Configuration

### 7.1 Embedded resources

Located under `internal/prompts/embedded/` and accessed via
`internal/prompts`:

- `system_old.txt`
- `system_new.txt`
- `system_revised.txt` (default)
- `CONTEXT.csv` — control catalog with `controlId`, `prompt_context`,
  and category metadata.

`make verify-prompts` (CI gate) fails if these have drifted from the
canonical sources fed by `make sync-prompts`.

### 7.2 Environment variables

| Var | Read by | Effect |
|---|---|---|
| `AWS_REGION` | `awsauth.LoadConfig` | Bedrock region. |
| `AWS_DEFAULT_REGION` | `awsauth.LoadConfig` | Fallback if `AWS_REGION` is unset. |
| `AWS_PROFILE` | `awsauth.LoadConfig` | Named profile. |
| `THOR_VERBOSE` | CLI root | Verbose logging. |
| `THOR_JSON` | CLI root | JSON output where applicable. |
| `THOR_INSTALL_DIR` | `make install-local` | Override install destination. |

No other Thor-specific environment variables exist. There is no
configuration file.

### 7.3 AWS credential resolution

Order is determined entirely by the AWS SDK default chain:

1. Environment variables (`AWS_ACCESS_KEY_ID`, etc.).
2. `~/.aws/credentials` (including SSO and `credential_process`).
3. `~/.aws/config` profiles.
4. EC2 / ECS instance metadata.

When the SDK reports `ExpiredToken`, `ExpiredTokenException`,
`TokenRefreshRequired`, or known SSO-expiry strings,
`awsauth.LoadConfig` returns
`fmt.Errorf("%w: %v\n\n%s", ErrExpiredCredentials, err, expiredCredsHelp)`.
The CLI maps that sentinel to exit 3 and prints the multi-line refresh
hint:

```
AWS credentials expired. To refresh, run one of:
  aws sso login --profile <profile>     # for SSO users
  aws configure                         # for static keys
  isengardcli assume <account>          # if you're on an Amazon Cloud Desktop
  ada credentials update                # alternative for Amazon Cloud Desktop users
```

## 8. Versioning and release metadata

`internal/version` exports `Version`, `Commit`, `BuildDate`, and a
`String()` helper. Values are injected at link time:

```
go build -ldflags "
  -s -w
  -X thor-golang/internal/version.Version=$(VERSION)
  -X thor-golang/internal/version.Commit=$(COMMIT)
  -X thor-golang/internal/version.BuildDate=$(DATE)
"
```

Both binaries print this via `--version`. The MCP server advertises
`Version` (short form) in `serverInfo.version`.

## 9. Library pins

| Concern | Library | Locked version |
|---|---|---|
| MCP SDK | `github.com/modelcontextprotocol/go-sdk` | `v1.6.0` |
| AWS Bedrock | `github.com/aws/aws-sdk-go-v2/service/bedrockruntime` | `≥v1.50.6` |
| AWS config | `github.com/aws/aws-sdk-go-v2/config` | latest |
| Excel | `github.com/xuri/excelize/v2` | latest |
| PDF split | `github.com/pdfcpu/pdfcpu` | latest |
| PDF text fallback | `github.com/ledongthuc/pdf` | latest |
| CLI | `github.com/spf13/cobra` | latest |
| Concurrency | `golang.org/x/sync` | latest |
| Image resize | `golang.org/x/image/draw` | latest |
| Logging | stdlib `log/slog` | — |
| Testing | stdlib `testing` + `github.com/google/go-cmp` | latest |

CGo is disabled (`CGO_ENABLED=0`). The binary cross-compiles cleanly
to all six target platforms (darwin amd64+arm64, linux amd64+arm64,
windows amd64+arm64).
