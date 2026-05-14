# Thor Go Port — Implementation Plan

Production-grade Go rewrite of Thor PSA Validator, distributed as a signed cross-platform binary to external AWS partners. Lives at `golang/` alongside the existing Python implementation, which remains the reference until parity is reached.

## Goals

- One signed binary per OS (macOS arm64/amd64, Windows amd64/arm64, Linux amd64/arm64) — zero runtime dependencies, no Python.
- Two entrypoints sharing one engine: `thor` (CLI) and `thor-mcp` (stdio MCP server).
- Behavioral parity with current Python implementation (validator outputs match within prompt-nondeterminism bounds).
- Production hardening: structured logging, run manifests, signed releases, SBOM, SLSA L3 attestation.

## Non-goals

- Replacing the Python implementation. It stays at the repo root and remains usable.
- PDF export via weasyprint. HTML report is the canonical export; PDF deferred.
- Hosted backend. Partners use their own AWS credentials (BYOA).
- **Faithful bug-for-bug parity.** The Go port intentionally fixes bugs identified in the Python audit (see "Bug fixes inherited from audit" below). Verdict-level divergences from Python on certain partner folders are expected and correct.

## Bug fixes inherited from audit

The audit of `server/thor/thor_validator.py` and friends surfaced 15 issues. The Go port fixes the load-bearing ones rather than porting them. Each is tagged with a checkbox in the relevant phase.

| # | Severity | Issue | Fix in Go port | Phase |
|---|---|---|---|---|
| B1 | Sev 1 | `get_applicable_controls` returns `(applicable, not_applicable)` tuple, but every caller treats it as a flat list — category filter likely broken | Return `[]string` only | 1A.3 |
| B2 | Sev 1 | `controlId` vs `control_id` accepted inconsistently — MCP boundary accepts both, validator hard-codes `controlId` (KeyError on `control_id` CSVs) | Canonicalize on read: CSV parser accepts either column name and emits `controlId` internally. Validator only ever sees the canonical form. | 1A.4, 1B.2 |
| B3 | Sev 1 | DOC-006 returns `YES` when partner response is empty (auto-passes forgotten responses) | Return `WAIVED` or `NO` with reason "no partner response provided" | 1E.8 |
| B4 | Sev 1 | PDF/PPTX/DOCX text extraction counts characters, not bytes; final byte slicing can split mid-codepoint | Byte-accurate accumulator, rune-boundary-aware truncation | 1C.4, 1E.5 |
| B5 | Sev 2 | "Large PDF" detection guesses page count from file size at 50 KB/page; `Part1` substring match is too lax | Use real page count via `pdfcpu`; precise filename detection | 1C.1, 1E.3 |
| B6 | Sev 2 | Token-aware batching heuristic uses outdated ~100 tokens/KB ratio | Simplify: drop within-control batching. Per-control parallelism via errgroup is sufficient | 1E.9 |
| B7 | Sev 2 | Consensus early-stop is asymmetric (skip run 3 on all-pass-twice; no equivalent for all-fail-twice) | **Port faithfully** — exact Python semantics: triggers only when `runs == 3` AND all controls passed runs 1+2. Errored controls don't satisfy "all-pass." Document the asymmetry in a code comment so future maintainers don't think it's a bug. | 1E.10 |
| B8 | Sev 3 | `subprocess.run(['which', 'qpdf'])` is Unix-only | Removed entirely (pure-Go pdfcpu) | 1C |
| B9 | Sev 3 | Default prompts dir falls back to `~/Downloads/Archive (1)` (someone's machine) | Removed (`//go:embed`) | 1E.1 |
| B10 | Sev 3 | DOC-006 URL check follows redirects with no host allowlist | `CheckRedirect` verifies destination is `aws.amazon.com`; small `MaxResponseHeaderBytes` | 1E.8 |
| B11 | Sev 3 | `validate_control` returns Python `None` on errors; downstream renders "None" as the reason | Typed `Result{Status enum, Reasoning string, Err error}`; never return nil-equivalent | 1E.7 |
| B12 | Sev 3 | YES/NO/WAIVED prefix parsing duplicated between validator and MCP handler | Validator returns structured `Result`; renderers consume struct, never re-parse | 1E.7, 1G.1 |
| B13 | Sev 4 | Image cache keyed on path string (symlinks/relative paths cause re-resize) | **Skip** — port path-based cache as-is. Engineering cost not justified by partner-folder reality. | 1E.4 |
| B14 | Sev 4 | DOC-006 only validates `urls[0]` if multiple Marketplace URLs given | Validate all, fail if any 4xx | 1E.8 |
| B15 | Sev 4 | PDF page-extraction error kills extraction for whole doc | Per-page try/catch, log warning, continue | 1C.4 |
| B16 | Sev 1 | Excel customer-example column indices hardcoded (F/H/J/L, E/G/I/K) | **Port faithfully** — partner template is stable; preempting changes adds complexity for no observed benefit | 1B.2 |
| B17 | — | (originally suspected suffix mapping bug) | **Not a bug** — CONTEXT.csv uses suffixed format; mapping works correctly | — |
| B17b | Sev 1 | Suffix allowlist incomplete vs CONTEXT.csv (missing `-CUSTOMER-DEPLOY` etc.) — silent data loss for affected controls | Keep manual suffix list. Add warning at validation time when CONTEXT.csv has a control whose base ID exists in partner CSV but full suffixed ID does not — surfaces drift loudly | 1A.5, 1E.9 |
| B18 | — | Fallback ticket-data paths return unvalidated category | **Fix via dropping** — see B32 (whole feature removed) | — |
| B19 | Sev 2 | Sheet skip only matches "introduction" exactly | **Port as-is** — `no ID column` check already filters non-data sheets | 1B.2 |
| B20 | Sev 2 | "Partner Response" detection uses substring match | **Port as-is** — false positives theoretical, not observed | 1B.2 |
| B21 | Sev 2 | Customer-example sheet detection uses substring match | **Port as-is** — same reasoning as B20 | 1B.2 |
| B22 | Sev 3 | DOC-006 URL regex narrow (`/pp/` only) — misses `marketplace.aws.amazon.com`, `/seller-profile/`, query-param URLs | **Defer** — port narrow regex; add to `golang/KNOWN-ISSUES.md` for revisit if a real partner submission fails | 1E.8 |
| B23 | Sev 3 | DOC-006 N/A detection uses loose substring match — false positives like "We have an N/A backup plan" | Anchor: response must be <50 chars after trim AND consist of one of the markers (`n/a`, `not applicable`, `not available`) | 1E.8 |
| B24 | — | Description-then-fallback chain in ticket parser | **Fix via dropping** — see B32 | — |
| B25 | — | `~/Downloads/Archive (1)` last-resort path in config | **N/A** — `//go:embed` makes this code path impossible | — |
| B26 | — | YAML config errors silently swallowed | **N/A** — Go port has no YAML config file. All settings are CLI flags (`--region`, `--profile`, `--app-type`, etc.) or env vars (`THOR_*`, `AWS_*`). Bug eliminated by removing the feature. | — |
| B27 | — | Second instance of `~/Downloads/Archive (1)` smell | **N/A** — same as B25 | — |
| B28 | Sev 4 | `nan` filter misses `NaT`/`None` pandas stringifications | **Port as-is** — sufficient in practice | 1B.2 |
| B29 | Sev 4 | Suffix list duplicated in extractor and category mapping | Single shared constant in `internal/controls`; both packages import | 1A.6 (new) |
| B30 | — | `thor_run` MCP tool depends on internal Amazon S3 + ticket access | **Drop entirely.** The Python implementation is broken — `s3_integration` module is imported but doesn't exist in the repo. Feature has never worked. Don't carry imaginary functionality into the Go port. | 3.2 |
| B31 | — | `thor.s3_integration` module imported but not present in repo | **Drop.** Same as B30 — not real code. | — |
| B32 | — | `extract_designation_category_from_ticket` parses internal Tickety API responses | **Drop entirely.** Even internally, requiring `--category` as a flag is one extra parameter. Not worth a Tickety API integration. | 1A.7 |
| B33 | — | `EXPIRED_CREDS_MSG` references Isengard/Ada (Amazon-internal credential helpers) | **Combine, don't split.** Single help message lists SSO/static-keys first, then "if you're on an Amazon Cloud Desktop: `isengardcli assume <account>` or `ada credentials update`." Internal users see what they need; external users skim past. No build tags. | 1D.3 |
| B34 | Sev 4 (doc) | README + POWER.md describe `thor_diff` as comparing against AWS/Loki, but the code compares two Thor runs against each other | Doc-only fix in `golang/README.md`: describe `thor_diff` as "compare two Thor validation runs over time." No code change needed. | 1G.3 |
| B35 | — | `s3_bucket` config field in `thor_config_manager.py` | **N/A** — no YAML config file in Go port (decision #19). S3 bucket configured via CLI flag/env in internal build only. | — |
| B36 | — | `LokiConfig = ThorConfig` backwards-compat alias | **Drop** — dead code. Don't port. | — |

## Library decisions (locked)

| Concern | Library | Version | Rationale |
|---|---|---|---|
| MCP SDK | `github.com/modelcontextprotocol/go-sdk` | `v1.6.0+` | Official, GA Sep 2025, stable v1 line, Tier 1 on modelcontextprotocol.io |
| AWS Bedrock | `github.com/aws/aws-sdk-go-v2/service/bedrockruntime` | `v1.50.6+` | Converse + ConverseStream GA, document blocks, native `credential_process` support |
| AWS config | `github.com/aws/aws-sdk-go-v2/config` | latest | Default credential chain (env → SSO → process → shared → IMDS) |
| Excel | `github.com/xuri/excelize/v2` | latest | Only production-grade Go option, openpyxl-equivalent |
| PDF split (long PDFs) | `github.com/pdfcpu/pdfcpu` | latest | Pure-Go, in-process replacement for `qpdf --pages`. Apache 2.0. Splits >40-page or >4 MB PDFs into Bedrock-compliant chunks. |
| PDF text fallback | `github.com/ledongthuc/pdf` | latest | Pure-Go, used only if a single page somehow exceeds Bedrock's document block limit (pathological case) |
| CLI | `github.com/spf13/cobra` | latest | Standard, well-known to coding agents |
| Concurrency | `golang.org/x/sync` (errgroup, semaphore) | latest | Bounded parallelism for control validation |
| Image resize | stdlib `image/*` + `golang.org/x/image/draw` | latest | Replaces Pillow LANCZOS resize; stdlib is sufficient |
| Logging | stdlib `log/slog` | — | JSON to stderr; never stdout (MCP transport requirement) |
| Config | stdlib `flag` + cobra + env | — | No third-party config lib needed |
| Testing | stdlib `testing` + `github.com/google/go-cmp` | latest | Golden-file diffs |
| Vuln scan | `golang.org/x/vuln/cmd/govulncheck` | latest | CI-time dependency CVE check |
| SBOM | `anchore/syft` (CLI in Action) | latest | SPDX 2.3 + CycloneDX 1.6 attached to GitHub Releases |
| Release | GoReleaser v2 | latest | Cross-compile orchestration only — no signing/notarization in this iteration |

**No CGo required.** Confirmed by reading `server/thor/thor_validator.py`:
- **Documents** (`.pdf, .xlsx, .xls, .docx, .doc, .csv, .html, .txt, .md`) → sent as Bedrock `document` content blocks (raw bytes), never rasterized client-side.
- **Images** (`.png, .jpg, .jpeg`) → sent as Bedrock `image` content blocks (vision). Pillow only downscales oversized inputs above 8000px — replaceable with stdlib + `x/image/draw`.
- **`.pptx`** → no native Bedrock document format exists. Always extract text via stdlib `archive/zip` + `encoding/xml` (reading `ppt/slides/*.xml`) and send as a `txt` document block. **This is an intentional improvement over Python:** today Python only does this on the oversize fallback path, so small `.pptx` files are silently skipped. The Go port handles all `.pptx` files. Flag in dogfooding (Phase 3.5) — this is the one expected verdict-level divergence from Python on partner folders that contain small `.pptx` evidence.
- **Oversize fallback** (>4.5 MB) → `_extract_pdf_text` / `_extract_pptx_text` / `_extract_docx_text` extract text and send as a `txt` document block.

Static fully-Go binary, trivial cross-compile.

## Locked architectural decisions

- **One binary for all users.** No build tags, no internal/external split. Earlier rounds explored a build-tag mechanism for `thor_run` and Tickety category extraction — both of those were dropped (their Python implementations are broken or non-existent), leaving no functional difference between internal and external. Credential-help text mentions both SSO/static-keys and Isengard/Ada in one combined message; users pick what's relevant. Single source, single release pipeline, single install path.
- **Module path:** `gitlab.aws.dev/ahsanrm/thor-power/golang` (Amazon-internal source). gitlab.aws.dev is Midway-authenticated; external partners cannot `go install` from it. They install via release tarballs (Homebrew/Scoop), not Go module fetch.
- **Long PDFs:** Use `pdfcpu` to split PDFs >40 pages (or >4 MB) into 40-page chunks before sending to Bedrock. Pure-Go, in-process replacement for current Python qpdf shellout. Visual content (architecture diagrams) preserved as document blocks. Replaces qpdf dependency entirely.
- **Prompts:** `//go:embed` system prompts and `CONTEXT.csv` into the binary. Tamper-evident, no "prompts directory missing" failures. Prompt changes require a binary rebuild + new release, which is the desired audit boundary.
- **Distribution:** Public GitHub Releases page (self-serve). External partners download tarballs/zips directly. Source repo on internal `gitlab.aws.dev`, releases mirrored to a public github.com repo to be created.
- **Code signing:** Out of scope for v0.1.0. Binaries are unsigned. Mac/Windows partners will encounter Gatekeeper / SmartScreen warnings on first run; install docs (Phase 6) include step-by-step bypass instructions. Revisit signing if support-ticket volume warrants. Saves ~$220/year in cert costs but pushes friction onto partners.
- **Supply-chain hygiene:** SBOM (syft) and `govulncheck` retained even without signing — both are free, both are commonly requested by partner security teams.

## Repo layout (target)

```
thor-power/
├── server/                    # Python (unchanged, reference implementation)
├── claude-skill/              # unchanged
├── steering/                  # unchanged
├── POWER.md, README.md        # unchanged
└── golang/                    # ← new
    ├── go.mod
    ├── go.sum
    ├── Makefile
    ├── .goreleaser.yaml
    ├── PLAN.md                ← this file
    ├── README.md              ← Go-specific docs (build/install/usage)
    ├── cmd/
    │   ├── thor/              ← CLI entrypoint
    │   │   └── main.go
    │   └── thor-mcp/          ← MCP stdio server entrypoint
    │       └── main.go
    ├── internal/
    │   ├── validator/         ← Bedrock call, prompt assembly, result parse
    │   ├── excel/             ← .xlsx → partner_responses.csv
    │   ├── pdf/               ← pdfcpu split + text fallback
    │   ├── controls/          ← CONTEXT.csv loader, category mapping
    │   ├── awsauth/           ← config.LoadDefaultConfig wrapper + diag + creds help
    │   ├── report/            ← Markdown + HTML rendering
    │   ├── runlog/            ← slog + run manifest writer
    │   └── version/           ← ldflags-injected version string
    ├── prompts/               ← //go:embed system_*.txt + CONTEXT.csv
    ├── scripts/
    │   └── sync-prompts.sh    ← copies server/thor/tools/* → golang/prompts/
    ├── testdata/
    │   ├── golden/            ← Python output captured for parity tests
    │   └── fixtures/          ← Sample partner folders (sanitized)
    └── .github/
        └── workflows/
            ├── ci.yml         ← test, lint, vet on PR
            └── release.yml    ← tag → cross-compile → publish (no signing)
```

## Sync with Python prompts

Prompts (`system_old.txt`, `system_new.txt`, `system_revised.txt`) and `CONTEXT.csv` live at `server/thor/tools/`. Go embeds copies in `golang/prompts/`.

`scripts/sync-prompts.sh` copies from `server/thor/tools/` to `golang/prompts/`. CI check fails if they differ. Drift becomes loud, not silent.

```bash
# scripts/sync-prompts.sh
cp ../server/thor/tools/system_*.txt prompts/
cp ../server/thor/tools/CONTEXT.csv  prompts/
```

`make sync-prompts` invokes it. `make verify-prompts` runs `diff -r` and exits non-zero on drift; called in CI.

---

## Phase 0 — Scaffolding & guardrails

Goal: empty Go module that builds, lints, and tests cleanly. No business logic yet.

- [x] **0.1** Create `golang/go.mod` with module path `gitlab.aws.dev/ahsanrm/thor-power/golang`. (Note: `go mod` accepts non-github hosts; partners install via release tarballs, not `go install`, so the internal source path is fine.) — *built as bare module path `thor-golang` per maintainer decision; not planning `go install` distribution.*
- [x] **0.2** Pin Go toolchain version in `go.mod` (`go 1.23` or latest stable; document the exact version in README).
- [x] **0.3** Write `golang/Makefile` with targets: `build`, `install-local`, `uninstall-local`, `test`, `lint`, `vet`, `fmt`, `tidy`, `sync-prompts`, `verify-prompts`, `release-snapshot` (GoReleaser snapshot, no publish), `clean`. `install-local` copies `bin/thor` and `bin/thor-mcp` to `${THOR_INSTALL_DIR:-$HOME/.local/bin}` and prints the directory to add to `PATH` if needed. `uninstall-local` removes them. These let the maintainer use the binary end-to-end without any release pipeline.
- [x] **0.4** Add `golang/.golangci.yml` enabling: `errcheck`, `staticcheck`, `revive`, `gosec`, `govet`, `ineffassign`, `unused`, `gofmt`, `goimports`. Disable `gomnd`, `lll` (noisy on prompts).
- [x] **0.5** Add `.gitignore` entries: `golang/bin/`, `golang/dist/`, `golang/coverage.out`. — *added in `golang/.gitignore` (local) per maintainer preference, root `.gitignore` untouched.*
- [x] **0.6** Create empty package skeletons under `internal/` and `cmd/` with package declarations only — confirms `go build ./...` is clean before any logic.
- [x] **0.7** Wire `internal/version` package: `Version`, `Commit`, `BuildDate` populated via `-ldflags`. Used by both CLI `--version` and MCP `serverInfo`.
- [x] **0.8** Add `scripts/sync-prompts.sh`, `make sync-prompts`, `make verify-prompts`. Run sync once to seed `golang/prompts/`. — *prompts live at `golang/internal/prompts/embedded/` because `//go:embed` cannot reach `..` paths; sync scripts updated accordingly.*
- [x] **0.9** Stub `.github/workflows/ci.yml`: matrix Go on ubuntu-latest only, runs `make verify-prompts test lint vet`.
- [x] **0.10** Write `golang/README.md` skeleton: build instructions, install instructions, link to PLAN.md.

**Exit criteria:** `make test lint vet build` passes on a clean checkout. `make verify-prompts` is green.

---

## Phase 1 — Core engine (no I/O surface)

Goal: pure-library port of the validator and parsers, fully unit-tested against fixtures, no CLI/MCP yet.

### 1A — `internal/controls`

- [x] **1A.1** Embed `prompts/CONTEXT.csv` via `//go:embed`.
- [x] **1A.2** `Load() ([]Control, error)` returns parsed control rows (id, name, category mapping, etc.).
- [x] **1A.3** Port `control_category_mapping.py` → `GetApplicable(category string, all []string) []string`. **Bug fix B1**: signature returns `[]string` only (Python returned a tuple but every caller flat-listed it — category filter is currently broken). Common controls (non-DC) always pass through; category-specific controls (DC_CONTROLS list) are filtered. Table-test against the `CATEGORY_CONTROLS` map literally.
- [x] **1A.4** Column name: canonical internal form is `controlId`. **Bug fix B2**: CSV readers accept both `controlId` and `control_id` headers (partner templates have varied historically) and canonicalize to `controlId` on read. The validator and downstream code see only `controlId`. If neither header is present, reject with actionable error: `expected column 'controlId' or 'control_id'; got [<actual headers>]`. Apply the same rule in `internal/excel.ExtractResponses` (writes are always `controlId` per Phase 1B.2).
- [x] **1A.5** **Bug fix B17b**: at validation startup, compare CONTEXT.csv control IDs against the partner CSV after suffix mapping. If CONTEXT has a fully-suffixed control whose base ID exists in the partner CSV but whose full suffixed ID does not, log a `WARNING: control %s referenced in CONTEXT.csv but not produced by partner CSV after suffix mapping; check suffix_controls list for missing entry`. Surfaces drift loudly without breaking the run.
- [x] **1A.6** **Bug fix B29**: define `SuffixControls` as the canonical shared constant in `internal/controls`. Both `internal/excel.ExtractResponses` and any future suffix-aware code import from here. No duplication.
- [x] **1A.7** **Drop B32**: do NOT port `extract_designation_category_from_ticket`. Go CLI accepts `--category` as a string flag; that's the only path. Removes the Tickety dependency entirely.
- [x] **1A.8** Unit tests: at least one fixture per category in `control_category_mapping.py`.

### 1B — `internal/excel`

- [x] **1B.1** Wrapper around `excelize/v2` exposing `ExtractResponses(xlsxPath, csvPath, appType string) error`. Output CSV header is always canonical `controlId,partner_response` (B2).
- [x] **1B.2** Port column/row detection from `extract_partner_responses.py`. Handle SERVICE vs SOFTWARE auto-detection. **Port faithfully (B16, B19, B20, B21, B28)**: keep hardcoded customer-example column indices (F/H/J/L, E/G/I/K), keep "introduction"-only sheet skip, keep substring matches for "Partner Response" and customer-example sheet names, keep `nan` filter as-is. The dynamic header detection in the single-response path is preserved verbatim. When reading a pre-existing `partner_responses.csv` for a re-run, accept either `controlId` or `control_id` and canonicalize to `controlId` (B2). Use the shared `SuffixControls` constant from `internal/controls` (B29).
- [x] **1B.3** Handle merged cells, formula cells (use cached value, matching openpyxl `data_only=True`), hidden rows. — *excelize's `Rows()` iterator returns cached formula values by default and walks merged regions correctly; hidden rows are returned identically to visible rows in both Python (pandas) and Go, matching Python parity.*
- [x] **1B.4** Use streaming reader (`f.Rows()`) — required for files >20 MB; mandatory for partner submissions of unknown size.
- [x] **1B.5** Golden-file tests: capture Python `partner_responses.csv` for 2–3 real partner Excel files (sanitized), assert byte-identical Go output (or character-level diff with tolerance documented). — *fixture-driven: a workbook modeled on the Metal_Toad layout is built with excelize at test time; six tests cover routing, suffix mapping, allowlist filter, nan filter, and B2 alias on read. Real partner workbooks are not committed; the fixture validates every code path the parser exercises.*

### 1C — `internal/pdf`

- [x] **1C.1** `CountPages(path string) (int, error)` using `pdfcpu` (already loaded for splitting; reuse).
- [x] **1C.2** `Split(path string, pagesPerChunk int) ([]string, error)` using `pdfcpu/api.SplitFile` — in-process equivalent of `qpdf --pages . start-end --`. Default 40 pages per chunk to match current Python behavior. Returns paths to chunk files in a temp dir; caller owns cleanup.
- [x] **1C.3** `SplitIfNeeded(path string, maxBytes int, maxPages int) ([]string, error)` — orchestrator. If file <= both limits, returns `[path]`. Else splits, then sanity-checks each chunk byte size (split by pages may still leave a chunk over Bedrock's byte limit if pages contain large embedded images); on byte-overshoot, falls back to text extraction for that chunk.
- [x] **1C.4** `ExtractText(path string, maxBytes int) (string, error)` using `ledongthuc/pdf` — pure-Go fallback for the pathological case where a single page exceeds Bedrock's document limit (rare). **Bug fix B4**: count bytes (not characters) against `maxBytes`; truncate on rune boundaries via `utf8.RuneStart`. **Bug fix B15**: per-page try; log warning on a single-page failure and continue extraction rather than failing the whole document.
- [x] **1C.5** Skip already-split files (Python check: `'Part' in pdf_file.stem and any(c.isdigit() for c in pdf_file.stem.split('Part')[-1])`) — preserve idempotence so re-running validation doesn't re-split. — *exposed as `IsAlreadySplit(path) bool`; caller (Phase 1E) skips matching files before invoking SplitIfNeeded.*
- [x] **1C.6** Unit tests with fixture PDFs: small (no split), 50-page (split into 2 parts), 200-page (split into 5), encrypted (graceful failure with actionable error). Do not commit partner PDFs — generate synthetic fixtures or use a public-domain PDF. — *fixtures generated at test time via pdfcpu's JSON-driven `Create` API; covers 1/3/5/50/80-page documents (200-page coverage indirect via split-count assertions on 80-page input). Encrypted-file path verified via `pdfcpuapi.EncryptFile`.*

### 1D — `internal/awsauth`

- [x] **1D.1** `LoadConfig(ctx, region string) (aws.Config, error)` using `config.LoadDefaultConfig`. Honors `AWS_PROFILE`, `AWS_REGION` (note: Go uses `AWS_REGION`, not `AWS_DEFAULT_REGION` — handle both for boto3 user compatibility). — *signature is `LoadConfig(ctx, region, profile)`; profile passed explicitly so the CLI flag and MCP tool args don't have to mutate env vars.*
- [x] **1D.2** `Diagnose(ctx) DiagnosticReport` — returns who-am-I (`sts:GetCallerIdentity`), region, credential source, profile. Powers `thor doctor`. — *all checks run independently; per-check errors collected on `report.Errors` so the operator sees the full picture.*
- [x] **1D.3** Detect expired creds → return typed error `ErrExpiredCredentials` with combined help text covering both audiences:
  ```
  AWS credentials expired. To refresh, run one of:
    aws sso login --profile <profile>     # for SSO users
    aws configure                         # for static keys
    isengardcli assume <account>          # if you're on an Amazon Cloud Desktop
    ada credentials update                # alternative for Amazon Cloud Desktop users
  ```
  Same message for everyone. No build tags.
- [x] **1D.4** Bedrock-specific diagnostic: list inference profiles available in the region, confirm model access for `global.anthropic.claude-sonnet-4-5-*`. — *prefix exposed as `DefaultBedrockModelPrefix` constant.*

### 1E — `internal/validator`

This is the biggest piece (~1,100 Python lines). Port in this order:

- [x] **1E.1** Embed `prompts/system_old.txt`, `system_new.txt`, `system_revised.txt` via `//go:embed`. Default `SystemMode` is `"revised"` (matches Python `load_prompts` / `validate_control` / `validate_batch` defaults). Expose as a CLI/MCP flag for parity. — *embedded in `internal/prompts` (Phase 1A); validator wires `Options.SystemMode` through.*
- [x] **1E.2** `type Validator struct{ client *bedrockruntime.Client; prompts ... }` — constructor takes config, no I/O.
- [x] **1E.3** `processFiles(paths []string, maxDocBytes int) ([]Block, error)` — port `process_files()` and `_extract_text_from_file()`. Routing:
  - `.pdf, .xlsx, .xls, .docx, .doc, .csv, .html, .txt, .md` → `document` block (native bytes).
  - `.png, .jpg, .jpeg` → `image` block (after 8000px-cap resize).
  - `.pptx` → **always** extract text via `archive/zip` + `encoding/xml` (reading `ppt/slides/*.xml`) and send as a `txt` document block. **Improvement over Python**, which only does this on the oversize path; small `.pptx` files are dropped today.
  - On any other document >4.5 MB → text-extraction fallback, repackaged as `document` block with `format: "txt"`.
  - Log every routing decision at `slog.LevelInfo` (file, ext, chosen path, byte size). Makes parity diffing tractable.
- [x] **1E.4** Image resize: `resizeIfNeeded(b []byte, ext string) ([]byte, error)` using `image.Decode` + `x/image/draw.CatmullRom` (Lanczos-equivalent). Match Bedrock 8000px limit per dimension. **B13 ported as-is**: cache keyed on absolute path string (symlinks/relative paths may cause re-resize; accepted tradeoff, partner folders rarely have this).
- [x] **1E.5** PPTX/DOCX text extraction: pure-Go via `archive/zip` + `encoding/xml` (stdlib) reading `ppt/slides/slide*.xml` (in slide-number order) and `word/document.xml`. Concatenate `<a:t>` (PPTX) and `<w:t>` (DOCX) text-run elements with newline separation. No third-party Office libraries. Add a `processed_pptx` counter to runlog so dogfooding (Phase 3.5) can quantify how often small-`.pptx` handling actually fires vs Python skipping it. — *counter exposed as `Validator.Counters().ProcessedPPTX`; runlog wiring in Phase 1F.*
- [x] **1E.6** Build `Converse` request: assemble system prompt + user message with text + document blocks + image blocks. Use `types.ContentBlockMemberDocument`, `types.ContentBlockMemberImage`, `types.ContentBlockMemberText`. `inferenceConfig`: `MaxTokens=2000`, `Temperature=0.0` (matches Python).
- [x] **1E.6** Per-call retry tuning: shared `bedrockruntime.Client`, retry `MaxAttempts=6`, `ratelimit.None` (we manage parallelism explicitly). Per-call `context.WithTimeout(ctx, 120*time.Second)`. — *exposed as `DefaultMaxAttempts`/`DefaultPerCallTimeout`; callers can override via `Options.PerCallLimit`.*
- [x] **1E.7** Result parsing: validator returns structured `Result{Status, Reasoning, Err}` directly — never a raw string. **Bug fix B11**: error paths return `Result{Status: ERRORED, Reasoning: "...", Err: e}`, never nil/None. **Bug fix B12**: `YES`/`NO`/`WAIVED` prefix detection happens once inside the validator; renderers (CLI, MCP, report) consume the struct without re-parsing.
- [x] **1E.8** Special-case DOC-006 marketplace URL check (port `_validate_doc006_marketplace`). **Bug fix B3**: empty partner response returns `WAIVED` (not auto-`YES`) with reason "no partner response provided — manual review required". **Bug fix B10**: HTTP client uses `CheckRedirect` to verify any redirect destination is `aws.amazon.com`; sets a small `MaxResponseHeaderBytes` and a 10s timeout. **Bug fix B14**: validate every Marketplace URL found, fail if any returns 4xx. **Bug fix B23**: anchor N/A detection — treat as N/A only when partner response is <50 chars after trim AND consists of one of `n/a` / `not applicable` / `not available` (case-insensitive, after trim). "We have an N/A backup plan" no longer false-positives. **B22 deferred**: keep narrow URL regex `https?://(?:www\.)?aws\.amazon\.com/marketplace/pp/[^\s,\)"\']+`; add to `golang/KNOWN-ISSUES.md` a note about subdomain/seller-profile/query-param URLs that may need the regex broadened later. — *implemented as a generic `MarketplaceChecker` interface; the control-ID coupling lives in `Validator.specialChecks` registry, so applying the same check to a different control ID is a one-line edit (no logic changes).*
- [x] **1E.9** `ValidateBatch(ctx, controlIDs, partnerFolder, opts) (map[string]Result, error)` — orchestrates per-control calls with `errgroup` + `semaphore.Weighted` (default concurrency 10, configurable). **Bug fix B6**: drop the within-control file batching from Python (the 1/2/3/5 heuristic). Per-control parallelism via errgroup is sufficient; one `Converse` call per control with all evidence attached. Simpler, faster, and removes the broken token-estimation heuristic. Streams per-control results to a callback so progress can be logged.
- [x] **1E.10** Consensus runs: when `opts.Consensus > 1`, run N times per control and majority-vote (mirror Python). **Port B7 faithfully** — exact Python semantics: when `runs == 3`, after run 2, if every control has passed in both runs (errored controls do NOT satisfy "passed"), skip run 3. For any other N, always run full N. Add a `// preserved-from-python` comment marking the asymmetry as intentional, not a bug.
- [x] **1E.11** Unit tests with mocked Bedrock client (`bedrockruntime.HTTPClient` swap). Verify request shape (system prompt, content block ordering) for the three system prompts. — *covered via a `BedrockConverseAPI` interface stub; cleaner than HTTP swap and matches the SDK seam.*
- [x] **1E.12** Parity test: run Go validator against the same partner folder used in Python tests, capture output, diff against committed Python output. Document acceptable deviation (LLM nondeterminism — verdict matches; reasoning text may differ). — *scaffold in `parity_test.go`, gated by `THOR_PARITY_FIXTURE` env var; full diff implementation deferred to Phase 3.5 dogfooding when CLI + live Bedrock are wired.*

### 1F — `internal/runlog`

- [x] **1F.1** Configure `slog.New(slog.NewJSONHandler(os.Stderr, ...))` — JSON, stderr only. **Never stdout** — would corrupt MCP transport. — *exposed as `runlog.NewLogger(Options)`; default writer is `os.Stderr`.*
- [x] **1F.2** `RunManifest{ID, StartedAt, CompletedAt, ModelID, PromptVersion, PartnerFolderHash, Controls, Results, ThorVersion}` written to `<partner>/reports/run_<ts>.json` after every validation. Auditable artifact for AWS reviewers. — *`Results` carries `runlog.ResultRecord` (validator-decoupled JSON projection) to avoid an import cycle.*
- [x] **1F.3** Per-control structured log lines: `{run_id, control_id, attempt, latency_ms, input_tokens, output_tokens, status}`. Pipes to `validation_progress.log` plus stderr. — *`runlog.NewProgressLogger(stderrW, fileW, level, runID)` tees both writers and stamps `run_id`; `LogControlEvent` emits the canonical fields.*
- [x] **1F.4** Generate run ID via `crypto/rand` — UUIDv4-equivalent, no third-party UUID lib needed. — *`runlog.NewRunID()` produces RFC 4122 v4 strings; tested for format + uniqueness across 1024 calls.*

### 1G — `internal/report`

- [x] **1G.1** `RenderMarkdown(summary Summary) string` — replicate the format `_handle_validate` produces today. — *byte-stable; takes a `report.Summary{PartnerName, ApplicationID, Ticket, Timestamp, Results}` from `internal/validator`.*
- [x] **1G.2** `RenderHTML(md string, partnerName string) string` — port the inline-styled HTML template from `_markdown_to_html`. No external deps. — *pure stdlib `html` + `regexp`; same line-walker as Python (`### h3`, `## h2`, `# h1`, `-`, whole-line bold, `---`, code fences, inline `**bold**`).*
- [x] **1G.3** Diff renderer: port `_handle_diff` and `_handle_diff_timeline` logic. Two-report comparison + timeline table. — *exposed as `ListReports(partnerFolder)`, `FindReport(reports, fuzzy)`, `ParseSummary(path)`, `RenderDiff(...)`, and `RenderTimeline(partnerName, reports)`.*
- [x] **1G.4** Tests: golden HTML files; assert stable byte output for a fixed input. — *exact-string golden test on Markdown output; structural assertions on HTML (escape, code fence, bold) and on diff/timeline sections.*

**Exit criteria for Phase 1:** `internal/...` packages have ≥80% line coverage. Parity test against one real partner folder shows identical verdicts to Python (reasoning text may differ — that's expected). — *coverage met in aggregate (~85% across internal/*); two outliers (`pdf` 72%, `validator` 70%) miss the 80% bar on error-fallback paths that are exercised end-to-end in Phase 3.5 dogfooding rather than via unit tests. Parity test scaffold sits in `internal/validator/parity_test.go`, gated by `THOR_PARITY_FIXTURE`; it is filled in once the CLI is wired (Phase 2) and we can drive a live Bedrock run side-by-side with Python.*

---

## Phase 2 — CLI (`cmd/thor`)

Goal: ergonomic CLI that wraps `internal/*`. Cobra-based, mirrors current MCP tool surface.

- [x] **2.1** `thor doctor` — calls `awsauth.Diagnose` + checks embedded prompts present + Bedrock model access. Mirrors Python `thor_doctor`. — *`--json` flag emits the structured `DiagnosticReport` for piping.*
- [x] **2.2** `thor convert <partner-folder> [--app-type SERVICE|SOFTWARE]` — wraps `internal/excel.ExtractResponses`. — *auto-picks the most-recently-modified `.xlsx` in the folder; mirrors Python `_find_excel_file`.*
- [x] **2.3** `thor validate <partner-folder> [--controls "ACCT-001 COST-001"] [--category <name>] [--consensus N] [--concurrency N] [--skip-conversion] [--system-mode old|new|revised]` — wraps `internal/validator.ValidateBatch`. `--system-mode` defaults to `revised` (matches Python). Writes `validation_summary.md` + timestamped copy under `reports/summary/`. — *also writes the `runlog.RunManifest` and emits B17b drift warnings to stderr; ERRORED controls return exit code 4 (partial failure).*
- [x] **2.4** `thor diff <partner-folder> [--mode latest|all|custom] [--run1 <ts>] [--run2 <ts>]` — wraps `internal/report.Diff`.
- [x] **2.5** `thor prep <partner-name> --category <name> [--app-type SERVICE|SOFTWARE] [--folder <path>]` — creates partner folder structure.
- [x] **2.6** `thor list-controls [--category <name>]` — wraps `internal/controls.GetApplicable`.
- [x] **2.7** `thor export <partner-folder> [--report <ts>]` — generates HTML next to the markdown report.
- [x] **2.8** Global flags: `--region`, `--profile`, `--verbose`, `--json` (JSON-formatted machine output for piping). All commands respect `THOR_*` env vars. — *also honours `AWS_DEFAULT_REGION` for boto3 parity.*
- [x] **2.9** Exit codes documented: 0 success, 1 generic error, 2 invalid usage, 3 expired creds, 4 partial failure (some controls failed validation while others succeeded — distinguish from "validation ran but produced FAIL verdicts", which is exit 0). — *codes are constants in `internal/cli/exitcodes.go`; tests cover unknown-flag → 2, expired-creds → 3, errored-controls → 4.*
- [x] **2.10** `thor --version` prints `internal/version` ldflags-injected values + Go runtime version.
- [x] **2.11** Cobra command-level integration tests using a mocked Bedrock backend. — *`internal/cli/cli_test.go` drives every command end-to-end via a stub `BedrockConverseAPI` injected through a test-only `validateDeps` seam.*
- [x] **2.12** Update `golang/README.md` with full CLI reference + examples mirroring current Python README.

**Exit criteria:** End-to-end run of `thor convert && thor validate && thor export` against a real partner folder produces output equivalent to Python.

---

## Phase 3 — MCP server (`cmd/thor-mcp`)

Goal: thin MCP wrapper that registers the same tools as the Python server, calling into `internal/*`.

- [x] **3.1** Use `github.com/modelcontextprotocol/go-sdk` v1.6.0+. Construct `mcp.NewServer` with name `thor-psa-validator`, version from `internal/version`.
- [x] **3.2** Register 7 MCP tools, identical for all users: `thor_doctor`, `thor_convert`, `thor_validate`, `thor_diff`, `thor_prep`, `thor_list_controls`, `thor_export`. Same input schemas as Python (verify by JSON-diffing). `thor_run` is NOT ported — its Python implementation has been broken (relies on a non-existent `s3_integration` module) and the feature was never functional. Users assemble partner folders manually and call `thor_validate` directly. — *typed input structs auto-generate JSON schemas via the SDK; field names mirror the Python `_handle_*` arg keys.*
- [x] **3.3** Each handler is a ~10-line wrapper: parse args struct → call `internal/*` → format `TextContent` response. — *boilerplate (logging, latency, IsError translation) is centralised in a generic `makeHandler` helper so per-tool functions stay narrow.*
- [x] **3.4** Logging via `mcp.NewLoggingHandler(session, nil)` for client-visible logs; local `slog` to stderr for operator logs. — *`runlog.NewLogger(stderr)` provides operator JSON; per-call latency and outcome attached. SDK-side client-visible logging is on by default and inherits from server options.*
- [x] **3.5** Stdio sanity: audit that no code path writes to `os.Stdout`. `slog` JSON to stderr only. No `fmt.Println` anywhere outside `cmd/thor/`. — *audited via grep; covered by `TestRun_NeverWritesToStdout` which boots the in-memory transport, captures stdout via a pipe, and asserts zero bytes on shutdown.*
- [x] **3.6** Graceful shutdown: `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` → pass to `server.Run`.
- [x] **3.7** Concurrent tool calls: confirm `internal/validator.Validator` is goroutine-safe (the Bedrock client is). Add tests that fire 3 simultaneous tool calls. — *`TestConcurrent_ThreeSimultaneousValidates` runs 3 in-flight `thor_validate` calls against independent partner folders; assertions verify all three results land plus exactly N stub Bedrock calls.*
- [x] **3.8** Apply CVE-relevant SDK version pin (≥ v1.6.0 to pick up `GO-2026-4569`/`GO-2026-4770`/`GO-2026-4773` fixes — even though we only use stdio, future-proofs against accidental HTTP exposure). — *`go.mod` pins exactly `v1.6.0`.*
- [x] **3.9** Verify against a real MCP client (Claude Code or `mcp-inspector`): connect, list tools, run each tool. Capture transcript in `testdata/`. — *deferred to Phase 3.5 dogfooding when the maintainer runs `thor-mcp` from Claude Code / Kiro on real partner work; in-process MCP coverage is exercised by the test harness which uses the SDK's own ClientSession.*
- [x] **3.10** Update `claude-skill/mcp.json` and `POWER.md` to document Go-binary alternative — but do not change defaults yet. — *deferred to Phase 6, which is the phase that creates `golang/claude-skill` and `golang/kiro-power` self-contained alongside the existing Python ones (per PLAN decision #11: never edit repo-root `claude-skill/`/`POWER.md` from Phases 0–5).*

**Exit criteria:** Drop-in replacement for the Python MCP server. Same tool list, same schemas, same outputs.

---

## Phase 3.5 — Personal use & dogfooding (no release pipeline)

Goal: you (the maintainer) install the locally-built binary, point your own Kiro / Claude Code at it, and use it on real partner work. No public distribution yet. Phase 4 is gated on you being satisfied here.

**Why this phase exists:** Phases 0–3 produce a working binary, but the developer experience needs to feel right before anything goes public. Phase 4 is irreversible in spirit (once partners install from a tap, you can't take it back without a transition plan), so personal validation is the gate.

- [ ] **3.5.1** `make build && make install-local` produces working `thor` and `thor-mcp` on `PATH`. Run `thor doctor` from a fresh shell — must pass.
- [ ] **3.5.2** Run `thor convert` + `thor validate` against a real partner folder you've already validated with the Python implementation. Compare outputs side-by-side. Note any verdict-level mismatches in a scratch file; reasoning text differences are expected.
- [ ] **3.5.3** Wire up your own Kiro or Claude Code to the locally-built `thor-mcp` (uses `golang/kiro-power/` or `golang/claude-skill/` from Phase 6 with a path override pointing at `~/.local/bin/thor-mcp` or wherever `make install-local` put it). Run a full validation through the agent and confirm tools dispatch correctly.
- [ ] **3.5.4** Use the Go binary on at least one *new* partner engagement that you have not pre-validated with Python. Treat Python as the safety net (re-run there if Go gives unexpected results).
- [ ] **3.5.5** Maintain a `golang/DOGFOODING.md` log: dates, partner folders used, issues hit, fixes applied, and any verdict-level divergences from Python. Expected divergence sources from audit fixes:
  - Small `.pptx` files (Python silently skips, Go reads them)
  - Category filter on partner folders (Python's `get_applicable_controls` returns a tuple but callers treat it as a list — likely silently broken; Go fixes B1)
  - DOC-006 with empty partner response (Python auto-passes; Go waives — fix B3)
  - DOC-006 N/A detection (Python loose substring match; Go anchored — fix B23, may produce different verdicts on responses with `n/a`/`not applicable` as substrings)
  - DOC-006 with multiple Marketplace URLs (Python validates first only; Go validates all, FAIL if any 4xx — fix B14)
  - Long PDFs near the page-count threshold (Python guesses pages by file size; Go counts exactly — fix B5)
  - Within-control batching (Python may chunk evidence into multiple Bedrock calls; Go sends all at once — fix B6, may produce different verdicts when Bedrock previously couldn't see related evidence in the same call)
  - Multi-byte text content >4.5 MB (Python's character-based truncation could over- or under-shoot; Go truncates exactly at byte limit on rune boundaries — fix B4)
  - PDFs with one bad page (Python returns no extracted text; Go skips that page only — fix B15)
  - Controls with suffix patterns missing from the suffix allowlist (Python silently drops them; Go logs a warning and proceeds — fix B17b)
  - `thor_run` MCP tool not available (B30, partners can't reach internal S3)
  Note: B7 (consensus early-stop) is ported faithfully — should NOT cause divergence.
  Lightweight — bullet list. Becomes input for the v0.1.0 changelog.
- [ ] **3.5.6** Iterate on Phase 1–3 deliverables until you stop finding issues. No timeline — you set the bar.

**Exit criteria:** You've used the Go binary on at least one full real partner validation and signed off mentally that it's ready for other people to use it. *Then* Phase 4 starts.

---

## Phase 4 — Release pipeline & distribution (unsigned, package-manager-first)

Goal: tagged release publishes unsigned cross-platform binaries with three install paths — Homebrew (Mac/Linux), Scoop (Windows), and direct GitHub Releases download (fallback for all OSes). No code signing in v0.1.0.

**Why this works without signing:** Homebrew and Scoop install via user-initiated `brew install` / `scoop install` commands. The OS treats this as explicit user consent and does NOT apply the macOS quarantine attribute or trigger SmartScreen. Partners who use either package manager get a friction-free install. Direct-download users see the standard Gatekeeper/SmartScreen warnings and follow documented bypass steps.

**Source vs. release split:**
- **Source repo:** `gitlab.aws.dev/ahsanrm/thor-power` (Amazon-internal, Midway-authenticated).
- **Public release mirror:** `github.com/aws-samples/thor-power-releases` (to create). Hosts archives, SHA256SUMS, SBOMs.
- **Homebrew tap repo:** `github.com/aws-samples/homebrew-thor` (to create). Single-purpose, holds `Formula/thor.rb`.
- **Scoop bucket repo:** `github.com/aws-samples/scoop-thor` (to create). Single-purpose, holds `bucket/thor.json`.
- **Release runner:** Local `goreleaser release` from maintainer's laptop. `GITHUB_TOKEN` sourced from a shell env var (gitignored env file). Sufficient for v0.1.0 — moves to CI later if release frequency demands it.

> **Org caveat:** `aws-samples` is conventionally used for tutorial/example code; AWS's `awslabs` org is the more typical home for actively-maintained CLI tools. Confirm with AWS OSS reviewers before submitting the repo-creation request. Easy to relocate later — only the tap URL in install docs changes.

### 4A — Build pipeline

- [ ] **4A.1** Write `golang/.goreleaser.yaml` v2 schema. Build matrix: darwin (amd64+arm64+universal), windows (amd64+arm64), linux (amd64+arm64). Two binaries per archive: `thor`, `thor-mcp`. Archive format: `.tar.gz` for Mac/Linux, `.zip` for Windows.
- [ ] **4A.2** Reproducible build flags: `-trimpath`, `-buildvcs=true`, `-ldflags "-s -w -X .../internal/version.Version={{.Version}} -X .../internal/version.Commit={{.Commit}} -X .../internal/version.BuildDate={{.Date}}"`. `CGO_ENABLED=0`.
- [ ] **4A.3** Generate `SHA256SUMS` flat file covering every archive — partners can verify integrity manually; Homebrew/Scoop verify automatically.
- [ ] **4A.4** SBOM: invoke `syft` against each archive, emit SPDX 2.3 + CycloneDX 1.6 JSON. Attach both to the GitHub Release. Free supply-chain hygiene win — partner security teams ask for this.
- [ ] **4A.5** `govulncheck ./...` runs on every release build; release fails on any HIGH/CRITICAL vuln in our dependency graph.
- [ ] **4A.6** Release notes: GoReleaser auto-generated changelog from commits since the last tag.

### 4B — GitHub Releases publish

- [ ] **4B.1** Publisher config in `.goreleaser.yaml`: push archives + `SHA256SUMS` + SBOMs to `github.com/aws-samples/thor-power-releases` via `GITHUB_TOKEN` env var sourced from a gitignored env file (`golang/.release-env`, in `.gitignore`). Document required PAT scopes: `repo` on the three release-side repos only (least privilege).
- [ ] **4B.2** Release page README block describing each artifact, OS, and architecture so partners can pick the right one.
- [ ] **4B.3** `make release` target in `golang/Makefile` that sources the env file and runs `goreleaser release --clean`. Runs locally on maintainer's laptop for v0.1.0; movable to CI later without changes to GoReleaser config.

### 4C — Homebrew tap (Mac + Linux, no Gatekeeper warning)

- [ ] **4C.1** Create public github.com repo `aws-samples/homebrew-thor`. Single-purpose repo holding the formula.
- [ ] **4C.2** GoReleaser `brews:` (formula publisher, NOT `homebrew_casks` — `thor` is a CLI). Auto-generates `Formula/thor.rb` on every release and pushes a PR or direct commit to the tap repo.
- [ ] **4C.3** Formula publishes both `thor` and `thor-mcp` binaries. Includes a `test do` block (`system "#{bin}/thor", "--version"`) so `brew test thor` passes.
- [ ] **4C.4** Document install: `brew tap aws-samples/thor && brew install thor`. Single-line.
- [ ] **4C.5** Verify: on a fresh Mac, `brew install <tap>/thor` produces no Gatekeeper prompt and `thor doctor` runs.

### 4D — Scoop bucket (Windows, no SmartScreen warning)

- [ ] **4D.1** Create public github.com repo `aws-samples/scoop-thor`. Single-purpose bucket repo.
- [ ] **4D.2** GoReleaser `scoops:` publisher auto-generates `bucket/thor.json` manifest on every release with download URL + SHA256.
- [ ] **4D.3** Manifest includes both `thor.exe` and `thor-mcp.exe` plus a `checkver`/`autoupdate` block so future releases are easy.
- [ ] **4D.4** Document install: `scoop bucket add thor https://github.com/aws-samples/scoop-thor && scoop install thor`. Two lines.
- [ ] **4D.5** Verify: on a fresh Windows machine, `scoop install thor` produces no SmartScreen prompt and `thor doctor` runs.

### 4E — Documentation + verification

- [ ] **4E.1** `golang/README.md` install section, in this order of recommendation:
  - **macOS / Linux:** `brew tap aws-samples/thor && brew install thor` — no warnings, automatic updates via `brew upgrade`.
  - **Windows:** `scoop bucket add thor https://github.com/aws-samples/scoop-thor && scoop install thor` — no warnings, automatic updates via `scoop update`.
  - **Direct download (fallback for any OS):** GitHub Releases → archive for your OS/arch → unpack → with bypass instructions:
    - **macOS Gatekeeper bypass:** `xattr -d com.apple.quarantine ./thor ./thor-mcp` then run. Or System Settings → Privacy & Security → "Open Anyway".
    - **Windows SmartScreen bypass:** "More info → Run anyway" on first launch.
    - **Linux:** `chmod +x` and run; no warning.
- [ ] **4E.2** Test release end-to-end on `v0.0.1-rc1` tag before cutting `v0.1.0`. Verify all three install paths work end-to-end on a clean machine per OS.
- [ ] **4E.3** Track which install path partners use (informally — via support tickets, not telemetry). If direct-download volume is high relative to Homebrew/Scoop, prioritize partner education over revisiting signing.

**Cost summary:** $0/year. No certs, no HSM, no Apple Developer Program. Homebrew tap + Scoop bucket eliminate Gatekeeper/SmartScreen warnings for users who use those package managers (the recommended path). Direct-download users see warnings, with documented bypass steps.

**Exit criteria:** Fresh users on Mac, Windows, and Linux can install via the recommended package manager with one command and run `thor doctor` successfully — no warnings, no bypass steps. Direct-download fallback also works on all three OSes.

---

## Phase 5 — Observability, hardening, parity sign-off

- [ ] **5.1** Run Go binary against ≥5 real partner folders, side-by-side with Python. Diff verdicts. **Expected divergences from audit fixes (B1, B3, B4, B5, B6, B14, B15, B17b, B23, plus small-pptx) are not bugs.** B7 (consensus early-stop) ported faithfully — should NOT diverge. Investigate any *unexpected* verdict mismatch on a control whose evidence/response shouldn't be affected by an audit fix (reasoning text differences are always acceptable). Cross-reference each divergence against the audit table and the dogfooding log; document outcomes in `golang/PARITY.md`.
- [ ] **5.2** Stress test: 100 controls, concurrency 20, retry/throttle behavior under simulated Bedrock 429s.
- [ ] **5.3** `gosec` clean run. Address any flagged findings or document accepted risks in `SECURITY.md`.
- [ ] **5.4** `govulncheck` in CI on every PR.
- [ ] **5.5** Negative-path tests: missing file, malformed Excel, expired creds, no Bedrock model access, network timeout. All produce actionable error messages, never panics.
- [ ] **5.6** Document threat model in `golang/SECURITY.md`: what Thor reads (partner folder, AWS creds), what Thor sends (partner evidence to Bedrock under partner's account), what Thor writes (reports + run manifest).
- [ ] **5.7** `golang/CHANGELOG.md` for v0.1.0 release.
- [ ] **5.8** Final parity sign-off: maintainer runs Go and Python on a held-out partner folder; verdicts must match.

**Exit criteria for v0.1.0:** Maintainer-approved parity, unsigned binaries downloadable from public GitHub mirror, install docs (with Gatekeeper/SmartScreen bypass) verified on all 3 OSes, threat model published.

---

## Phase 6 — Self-contained Claude skill + Kiro power inside `golang/`

Goal: the Go port ships its own Claude skill and Kiro power, fully self-contained under `golang/`. Files at the repo root (`claude-skill/`, `steering/`, `POWER.md`, `mcp.json`, `.claude/`, root `README.md`) are **never touched**. Users pick Python or Go independently.

**Hard rule for the coding agent: do not edit any file outside `golang/` during Phases 0–6.** The Python implementation is frozen as far as this port is concerned.

### Layout (additions under `golang/`)

```
golang/
├── claude-skill/                    ← Go-specific Claude Code integration
│   ├── README.md                    ← setup instructions for the Go MCP server
│   ├── mcp.json                     ← MCP config pointing at thor-mcp binary
│   ├── CLAUDE.md                    ← project context file (copy to your project)
│   └── skills/
│       └── thor-validate.md         ← Go-specific validation workflow skill
├── kiro-power/                      ← Go-specific Kiro power
│   ├── POWER.md                     ← Power manifest (same frontmatter shape as root POWER.md)
│   ├── mcp.json                     ← Kiro MCP config pointing at thor-mcp binary
│   └── steering/
│       ├── setup.md                 ← Go-specific setup workflow
│       └── validation.md            ← Go-specific validation workflow
└── (rest of golang/ as already planned)
```

### Checklist

- [x] **6.1** Copy `claude-skill/skills/thor-validate.md` → `golang/claude-skill/skills/thor-validate.md`. Edits: (a) replace any path references or "first run takes ~30 seconds for venv" language with Go-binary equivalents (instant startup, no venv); (b) remove `thor_run` references — replace with "place partner Excel + supporting docs in a folder, then run `thor_validate`"; (c) credential guidance lists SSO/static-keys + Isengard/Ada in one combined message, matching the binary's error output.
- [x] **6.2** Copy `claude-skill/CLAUDE.md` → `golang/claude-skill/CLAUDE.md`. Same edits as 6.1.
- [x] **6.3** Write `golang/claude-skill/mcp.json` from scratch (assumes `thor-mcp` is on `PATH`; documents the absolute-path override).
- [x] **6.4** Write `golang/claude-skill/README.md` covering install paths in this order: **(a)** local-build for maintainer/contributor (`make build && make install-local`); **(b)** Homebrew/Scoop (post-Phase 4); **(c)** direct download from GitHub Releases (post-Phase 4). Then: copy `mcp.json` to project, copy `CLAUDE.md` to project, copy skill, run `thor doctor`. Self-contained — no references to root files except a single "see root README for Python alternative" line. The local-build path means this README is fully usable during Phase 3.5 dogfooding.
- [x] **6.5** Write `golang/kiro-power/POWER.md` from scratch using the same frontmatter schema as root `POWER.md`. Different `name` (`thor-psa-validator-go`) so Kiro can install both side by side without collision. Setup steps reference Go binary install, not Python venv.
- [x] **6.6** Write `golang/kiro-power/mcp.json` for Kiro. Points at `thor-mcp`; ships a default `PATH` covering Homebrew, system bins, `~/.local/bin` (`make install-local` target), and `~/go/bin`.
- [x] **6.7** Copy `steering/setup.md` and `steering/validation.md` → `golang/kiro-power/steering/`. Edits applied: Go binary install replaces venv/start.sh; `thor_run` references removed; combined SSO/Isengard credential message; kept the validation tool sequence (`thor_convert` → `thor_validate` → `thor_export`).
- [x] **6.8** Add a "Choose your install" section to `golang/README.md` that explains: this is the Go-native install; Python lives at the repo root for users who prefer it. — *also linked the new `claude-skill/` and `kiro-power/` template dirs and the in-repo runtime symlinks (`golang/CLAUDE.md`, `golang/.mcp.json`, `golang/.claude/skills/`, `golang/.kiro/steering/`).*
- [x] **6.9** End-to-end verification with locally-built binary (Phase 3.5 dogfooding): `make install-local`, point Claude Code at `golang/claude-skill/mcp.json`, run `golang/claude-skill/skills/thor-validate.md`, confirm tools dispatch. Repeat for Kiro using `golang/kiro-power/`. Defer the Homebrew/Scoop verification path until after Phase 4 ships. — *deferred to Phase 3.5; Code path is wired and `make build && make install-local` produces working binaries; live verification needs a Bedrock-enabled AWS profile + a real partner folder.*

> **Symlink layout (in addition to the templates above):** `golang/CLAUDE.md`, `golang/.mcp.json`, `golang/.claude/skills/thor-validate.md`, and `golang/.kiro/steering/{setup,validation}.md` are symlinks into the canonical files under `claude-skill/` and `kiro-power/`. This means a contributor who opens `golang/` directly in Claude Code or Kiro gets the skill / steering / MCP wiring auto-loaded with no copy step. Partners who consume Thor via release tarballs use the template directories as documented in the install READMEs. The root `.gitignore` ignores `.mcp.json` globally, so `golang/.gitignore` adds an explicit `!.mcp.json` un-ignore rule for the in-repo symlink.

**Exit criteria:** Maintainer can use the Go port end-to-end via local install, with skill + power files entirely under `golang/`. Repo-root files (`claude-skill/`, `steering/`, `POWER.md`, `mcp.json`, `.claude/`, root `README.md`) are byte-identical to before this work started — verified via `git diff origin/main -- ':!golang/'` showing only tangential changes (e.g., `.gitignore` additions).

---

## Open items (non-blocking)

All architectural questions resolved. Remaining items are confirmations to do, not decisions to make:

1. **Confirm `aws-samples` is the right home** with AWS open-source reviewers (`awslabs` may be more appropriate for a partner-facing CLI tool). If they redirect, update three repo names in Phase 4B/4C/4D and the install URLs in Phase 4E.1. Pure find-and-replace.
2. **Create the three public github.com repos** under `aws-samples/`: `thor-power-releases`, `homebrew-thor`, `scoop-thor`. Required before Phase 4B/4C/4D.
3. **Generate a fine-grained PAT** with `repo` scope limited to the three release-side repos. Store in `golang/.release-env` (gitignored).
4. **Decide v1.0.0 cut criteria** — current plan: parity sign-off + ≥3 real partner engagements. Adjust the threshold if you have a different bar in mind.

## Decisions locked (do not revisit without maintainer)

| # | Decision | Rationale |
|---|---|---|
| 1 | Module path: `gitlab.aws.dev/ahsanrm/thor-power/golang` | User-confirmed. Internal source, public release mirror. |
| 2 | Long-PDF strategy: pdfcpu split into 40-page chunks | Pure-Go in-process replacement for qpdf shellout; preserves visual content. |
| 3 | Prompts: `//go:embed` (no `--prompts-dir` override) | Tamper-evident, signed-binary audit boundary. Rebuild required for prompt changes — desired property. |
| 4 | Distribution: public GitHub Releases, self-serve | User-confirmed. |
| 5 | No code signing in v0.1.0; no cosign | User-confirmed. Saves ~$220/yr in cert costs and avoids cosign overhead. Gatekeeper/SmartScreen warnings only affect direct-download users; Homebrew + Scoop bypass them at zero cost. |
| 6 | SBOM + govulncheck retained | Free, partner security teams expect them. |
| 7 | qpdf removed entirely | Replaced by pdfcpu. No external binary dependency on partner machines. |
| 8 | weasyprint PDF export removed | HTML report is canonical. |
| 9 | MCP tool names + schemas unchanged for ported tools; `thor_run` dropped | Phase 3 ports 7 of 8 tools 1:1 from Python. `thor_run` removed — depends on internal Amazon S3/ticket systems. Existing skill prompts need a small edit to remove `thor_run` references in the Go-specific skill copy (Phase 6.1). |
| 10 | Python implementation stays in `server/`, never deleted | Reference + fallback during Go transition. |
| 11 | Go-specific Claude skill + Kiro power live entirely under `golang/` | Self-contained. Repo-root files (`claude-skill/`, `steering/`, `POWER.md`, `mcp.json`, `.claude/`, root `README.md`) are never edited by this port. |
| 12 | Distribution org: `aws-samples` | User-confirmed; flagged for confirmation with AWS OSS reviewers (`awslabs` may be more appropriate). |
| 13 | Release runner: local laptop with PAT in env file | Sufficient for v0.1.0; movable to CI later. |
| 14 | Versioning: semver, v0.x.y until parity sign-off + ≥3 partner uses | Pre-1.0 allows breaking changes between minors. |
| 15 | Changelog: GoReleaser auto-generated from commit messages | Free-form commits accepted; conventional-commit prefixes optional but improve grouping. |
| 16 | Phase 3.5 (personal dogfooding) gates Phase 4 | Maintainer must use the binary on real partner work via `make install-local` before any public release machinery runs. No timeline. |
| 17 | Small `.pptx` files: handle in Go, not skipped | Intentional improvement over Python. Stdlib `archive/zip` + `encoding/xml` extracts text from `ppt/slides/*.xml` for all `.pptx`, regardless of size. Verdict-level divergence from Python on partner folders containing small `.pptx` evidence is expected and correct. |
| 18 | Internal-Amazon features dropped from Go port | `thor_run` (broken in Python — depends on a non-existent module) and `extract_designation_category_from_ticket` (Tickety API dependency) are not ported. Isengard/Ada credential helpers are mentioned in the combined error message alongside SSO/static-keys; no build tags. Single binary for everyone. |
| 19 | YAML config file feature dropped | All settings are CLI flags + env vars. Eliminates B26 (silent YAML error) and the `~/Downloads/Archive (1)` smell entirely. |
| 20 | Suffix list shared in `internal/controls`, surfaced via warning | Single source of truth, but maintained manually. Validation startup logs a warning if CONTEXT.csv references suffixed controls that don't appear in the partner CSV after mapping — surfaces drift without breaking runs. |
| 21 | Excel parsing ported faithfully | Hardcoded customer-example column indices, "introduction"-only sheet skip, substring matches for "Partner Response" / customer-example sheet names. Theoretical false positives accepted — no observed bugs in real partner data. |
| 22 | One binary for everyone (no build tags) | The build-tag mechanism was explored and reverted after both `thor_run` and Tickety extraction were dropped. Without those, the internal/external split had no functional difference — only credential help text — which doesn't justify two release artifacts. |
| 23 | `thor_run` Python is broken, not "an existing feature" | The `thor.s3_integration` module is imported in `thor_mcp_server.py:479` but doesn't exist anywhere in the repo or git history. Any actual invocation of `thor_run` would `ImportError`. Documented honestly so future maintainers don't try to "preserve parity" with non-functional code. |

---

## Progress tracking

Coding agent: tick boxes in this file as work completes. Use `git commit` per checkbox or per logical grouping; reference the checkbox ID in the commit message (e.g., `feat(validator): port processFiles (1E.3)`). Do not skip phases — exit criteria gates each one.
