# Thor — Implementation Plan

Remaining roadmap for the Thor PSA Validator. The engine, CLI, MCP server, and agent integrations are complete and shipped — see `SPEC.md` for the canonical behavioural spec and `README.md` for the user-facing surface. This file tracks only the work that is still pending.

## Goals

- One signed binary per OS (macOS arm64/amd64, Windows amd64/arm64,
  Linux amd64/arm64) — zero runtime dependencies.
- Two entrypoints sharing one engine: `thor` (CLI) and `thor-mcp`
  (stdio MCP server).
- Production hardening: structured logging, run manifests, signed
  releases, SBOM, SLSA L3 attestation.

## Non-goals

- PDF export via WeasyPrint or similar. HTML is the canonical export
  format; PDF deferred.
- Hosted backend. Partners use their own AWS credentials (BYOA).

## Locked architectural decisions

- **One binary for all users.** No build tags, no internal/external
  split. Credential-help text mentions both SSO/static-keys and
  Isengard/Ada in one combined message. Single source, single release
  pipeline, single install path.
- **Long PDFs:** `pdfcpu` splits PDFs >40 pages (or >4 MB) into
  40-page chunks before sending to Bedrock. Pure-Go, in-process.
  Visual content (architecture diagrams) preserved as document
  blocks.
- **Prompts:** `//go:embed` system prompts and `CONTEXT.csv` into
  the binary. Tamper-evident, no "prompts directory missing"
  failures. Prompt changes require a binary rebuild + new release,
  which is the desired audit boundary.
- **Distribution:** Public GitHub Releases page (self-serve). External
  partners download tarballs/zips directly. Releases mirrored to a
  public github.com repo (TBD — see Phase 4 open items).
- **Code signing:** Out of scope for v0.1.0. Binaries are unsigned.
  macOS/Windows partners using direct download will encounter
  Gatekeeper / SmartScreen warnings on first run; install docs include
  bypass instructions. Homebrew + Scoop install paths bypass these
  warnings entirely.
- **Supply-chain hygiene:** SBOM (syft) and `govulncheck` retained
  even without signing — both are free, both are commonly requested by
  partner security teams.

## Library decisions (locked)

| Concern | Library | Version | Rationale |
|---|---|---|---|
| MCP SDK | `github.com/modelcontextprotocol/go-sdk` | `v1.6.0+` | Official, stable v1 line. |
| AWS Bedrock | `github.com/aws/aws-sdk-go-v2/service/bedrockruntime` | `v1.50.6+` | Converse + ConverseStream GA, document blocks, native `credential_process` support. |
| AWS config | `github.com/aws/aws-sdk-go-v2/config` | latest | Default credential chain. |
| Excel | `github.com/xuri/excelize/v2` | latest | Production-grade Go option. |
| PDF split | `github.com/pdfcpu/pdfcpu` | latest | Pure-Go, in-process. Apache 2.0. |
| PDF text fallback | `github.com/ledongthuc/pdf` | latest | Pure-Go, used only on the pathological-page fallback. |
| CLI | `github.com/spf13/cobra` | latest | Standard, well-known to coding agents. |
| Concurrency | `golang.org/x/sync` (errgroup, semaphore) | latest | Bounded parallelism. |
| Image resize | stdlib `image/*` + `golang.org/x/image/draw` | latest | Stdlib is sufficient. |
| Logging | stdlib `log/slog` | — | JSON to stderr; never stdout (MCP transport). |
| Testing | stdlib `testing` + `github.com/google/go-cmp` | latest | Golden-file diffs. |
| Vuln scan | `golang.org/x/vuln/cmd/govulncheck` | latest | CI-time dependency CVE check. |
| SBOM | `anchore/syft` | latest | SPDX 2.3 + CycloneDX 1.6 attached to GitHub Releases. |
| Release | GoReleaser v2 | latest | Cross-compile orchestration. |

**No CGo required.** Static fully-Go binary, trivial cross-compile.

---

## Phase 3.5 — Personal use & dogfooding (no release pipeline)

Goal: maintainer installs the locally-built binary, points their own
Kiro / Claude Code at it, and uses it on real partner work. No public
distribution yet. Phase 4 is gated on the maintainer being satisfied
here.

**Why this phase exists:** the binary is feature-complete, but the
developer experience needs to feel right before anything goes public.
Phase 4 is irreversible in spirit (once partners install from a tap,
you can't take it back without a transition plan), so personal
validation is the gate.

- [ ] **3.5.1** `make build && make install-local` produces working
      `thor` and `thor-mcp` on `PATH`. Run `thor doctor` from a fresh
      shell — must pass.
- [ ] **3.5.2** Run `thor convert` + `thor validate` against a real
      partner folder. Capture verdicts and reasoning text in a
      scratch file for later comparison.
- [ ] **3.5.3** Wire up Kiro or Claude Code to the locally-built
      `thor-mcp` (uses `golang/kiro-power/` or
      `golang/claude-skill/` with a path override pointing at
      `~/.local/bin/thor-mcp` or wherever `make install-local` put
      it). Run a full validation through the agent and confirm tools
      dispatch correctly.
- [ ] **3.5.4** Use the binary on at least one new partner engagement
      end-to-end.
- [ ] **3.5.5** Maintain a `golang/DOGFOODING.md` log: dates, partner
      folders used, issues hit, fixes applied. Lightweight — bullet
      list. Becomes input for the v0.1.0 changelog.
- [ ] **3.5.6** Iterate on the engine until you stop finding issues.
      No timeline — maintainer sets the bar.

**Exit criteria:** Maintainer has used the binary on at least one full
real partner validation and signed off mentally that it's ready for
other people to use it. *Then* Phase 4 starts.

---

## Phase 4 — Release pipeline & distribution (unsigned, package-manager-first)

Goal: tagged release publishes unsigned cross-platform binaries with
three install paths — Homebrew (Mac/Linux), Scoop (Windows), and direct
GitHub Releases download (fallback for all OSes). No code signing in
v0.1.0.

**Why this works without signing:** Homebrew and Scoop install via
user-initiated `brew install` / `scoop install` commands. The OS
treats this as explicit user consent and does NOT apply the macOS
quarantine attribute or trigger SmartScreen. Partners who use either
package manager get a friction-free install. Direct-download users see
the standard Gatekeeper/SmartScreen warnings and follow documented
bypass steps.

**Source vs. release split:**
- **Public release mirror:** `github.com/aws-samples/thor-power-releases`
  (to create). Hosts archives, `SHA256SUMS`, SBOMs.
- **Homebrew tap repo:** `github.com/aws-samples/homebrew-thor` (to
  create). Single-purpose, holds `Formula/thor.rb`.
- **Scoop bucket repo:** `github.com/aws-samples/scoop-thor` (to
  create). Single-purpose, holds `bucket/thor.json`.
- **Release runner:** Local `goreleaser release` from maintainer's
  laptop. `GITHUB_TOKEN` sourced from a shell env var (gitignored env
  file). Sufficient for v0.1.0 — moves to CI later if release
  frequency demands it.

> **Org caveat:** `aws-samples` is conventionally used for
> tutorial/example code; AWS's `awslabs` org is the more typical home
> for actively-maintained CLI tools. Confirm with AWS OSS reviewers
> before submitting the repo-creation request. Easy to relocate later
> — only the tap URL in install docs changes.

### 4A — Build pipeline

- [ ] **4A.1** Write `golang/.goreleaser.yaml` v2 schema. Build
      matrix: darwin (amd64+arm64+universal), windows (amd64+arm64),
      linux (amd64+arm64). Two binaries per archive: `thor`,
      `thor-mcp`. Archive format: `.tar.gz` for Mac/Linux, `.zip` for
      Windows.
- [ ] **4A.2** Reproducible build flags: `-trimpath`,
      `-buildvcs=true`,
      `-ldflags "-s -w -X .../internal/version.Version={{.Version}} -X .../internal/version.Commit={{.Commit}} -X .../internal/version.BuildDate={{.Date}}"`.
      `CGO_ENABLED=0`.
- [ ] **4A.3** Generate `SHA256SUMS` flat file covering every archive.
- [ ] **4A.4** SBOM: invoke `syft` against each archive, emit SPDX
      2.3 + CycloneDX 1.6 JSON. Attach both to the GitHub Release.
- [ ] **4A.5** `govulncheck ./...` runs on every release build;
      release fails on any HIGH/CRITICAL vuln.
- [ ] **4A.6** Release notes: GoReleaser auto-generated changelog
      from commits since the last tag.

### 4B — GitHub Releases publish

- [ ] **4B.1** Publisher config in `.goreleaser.yaml`: push archives +
      `SHA256SUMS` + SBOMs to
      `github.com/aws-samples/thor-power-releases` via `GITHUB_TOKEN`
      env var sourced from a gitignored env file
      (`golang/.release-env`, in `.gitignore`). Document required PAT
      scopes: `repo` on the three release-side repos only.
- [ ] **4B.2** Release page README block describing each artifact,
      OS, and architecture.
- [ ] **4B.3** `make release` target in `golang/Makefile` that
      sources the env file and runs `goreleaser release --clean`.

### 4C — Homebrew tap (Mac + Linux)

- [ ] **4C.1** Create public github.com repo
      `aws-samples/homebrew-thor`.
- [ ] **4C.2** GoReleaser `brews:` formula publisher.
      Auto-generates `Formula/thor.rb` on every release.
- [ ] **4C.3** Formula publishes both `thor` and `thor-mcp` binaries.
      Include a `test do` block (`system "#{bin}/thor", "--version"`).
- [ ] **4C.4** Document install:
      `brew tap aws-samples/thor && brew install thor`.
- [ ] **4C.5** Verify on a fresh Mac: `brew install` produces no
      Gatekeeper prompt and `thor doctor` runs.

### 4D — Scoop bucket (Windows)

- [ ] **4D.1** Create public github.com repo `aws-samples/scoop-thor`.
- [ ] **4D.2** GoReleaser `scoops:` publisher auto-generates
      `bucket/thor.json` on every release.
- [ ] **4D.3** Manifest includes both `thor.exe` and `thor-mcp.exe`
      plus a `checkver`/`autoupdate` block.
- [ ] **4D.4** Document install:
      `scoop bucket add thor https://github.com/aws-samples/scoop-thor && scoop install thor`.
- [ ] **4D.5** Verify on a fresh Windows machine: `scoop install`
      produces no SmartScreen prompt and `thor doctor` runs.

### 4E — Documentation + verification

- [ ] **4E.1** `golang/README.md` install section, in this order of
      recommendation:
  - **macOS / Linux:**
    `brew tap aws-samples/thor && brew install thor`.
  - **Windows:**
    `scoop bucket add thor https://github.com/aws-samples/scoop-thor && scoop install thor`.
  - **Direct download (fallback for any OS):** GitHub Releases →
    archive for your OS/arch → unpack → with bypass instructions:
    - **macOS Gatekeeper bypass:**
      `xattr -d com.apple.quarantine ./thor ./thor-mcp` then run.
    - **Windows SmartScreen bypass:** "More info → Run anyway" on
      first launch.
    - **Linux:** `chmod +x` and run; no warning.
- [ ] **4E.2** Test release end-to-end on `v0.0.1-rc1` tag before
      cutting `v0.1.0`. Verify all three install paths on a clean
      machine per OS.

**Cost summary:** $0/year. No certs, no HSM. Homebrew tap + Scoop
bucket eliminate Gatekeeper/SmartScreen warnings for users who use
those package managers. Direct-download users see warnings, with
documented bypass steps.

**Exit criteria:** Fresh users on Mac, Windows, and Linux can install
via the recommended package manager with one command and run
`thor doctor` successfully — no warnings, no bypass steps.

---

## Phase 5 — Observability, hardening, parity sign-off

- [ ] **5.1** Run binary against ≥5 real partner folders. Document
      verdicts and any anomalies in `golang/PARITY.md`.
- [ ] **5.2** Stress test: 100 controls, concurrency 20, retry/throttle
      behaviour under simulated Bedrock 429s.
- [ ] **5.3** `gosec` clean run. Address any flagged findings or
      document accepted risks in `SECURITY.md`.
- [ ] **5.4** `govulncheck` in CI on every PR.
- [ ] **5.5** Negative-path tests: missing file, malformed Excel,
      expired creds, no Bedrock model access, network timeout. All
      produce actionable error messages, never panics.
- [ ] **5.6** Document threat model in `golang/SECURITY.md`: what
      Thor reads (partner folder, AWS creds), what Thor sends (partner
      evidence to Bedrock under partner's account), what Thor writes
      (reports + run manifest).
- [ ] **5.7** `golang/CHANGELOG.md` for v0.1.0 release.

**Exit criteria for v0.1.0:** maintainer-approved sign-off, unsigned
binaries downloadable from public GitHub mirror, install docs (with
Gatekeeper/SmartScreen bypass) verified on all 3 OSes, threat model
published.

---

## Open items (non-blocking)

All architectural questions resolved. Remaining items are
confirmations to do, not decisions to make:

1. **Confirm `aws-samples` is the right home** with AWS open-source
   reviewers (`awslabs` may be more appropriate for a partner-facing
   CLI tool). If they redirect, update three repo names in
   Phase 4B/4C/4D and the install URLs in Phase 4E.1. Pure
   find-and-replace.
2. **Create the three public github.com repos** under `aws-samples/`:
   `thor-power-releases`, `homebrew-thor`, `scoop-thor`. Required
   before Phase 4B/4C/4D.
3. **Generate a fine-grained PAT** with `repo` scope limited to the
   three release-side repos. Store in `golang/.release-env`
   (gitignored).
4. **Decide v1.0.0 cut criteria** — current plan: maintainer sign-off
   plus ≥3 real partner engagements. Adjust the threshold if a
   different bar applies.

## Decisions locked (do not revisit without maintainer)

| # | Decision | Rationale |
|---|---|---|
| 1 | Long-PDF strategy: pdfcpu split into 40-page chunks | Pure-Go in-process; preserves visual content. |
| 2 | Prompts: `//go:embed` (no `--prompts-dir` override) | Tamper-evident, signed-binary audit boundary. Rebuild required for prompt changes — desired property. |
| 3 | Distribution: public GitHub Releases, self-serve | Maintainer-confirmed. |
| 4 | No code signing in v0.1.0; no cosign | Saves ~$220/yr in cert costs and avoids cosign overhead. Gatekeeper/SmartScreen warnings only affect direct-download users; Homebrew + Scoop bypass them at zero cost. |
| 5 | SBOM + govulncheck retained | Free, partner security teams expect them. |
| 6 | weasyprint PDF export removed | HTML report is canonical. |
| 7 | One binary for everyone (no build tags) | Single source, single release pipeline, single install path. |
| 8 | Distribution org: `aws-samples` (TBD) | Maintainer-confirmed; flagged for confirmation with AWS OSS reviewers. |
| 9 | Release runner: local laptop with PAT in env file | Sufficient for v0.1.0; movable to CI later. |
| 10 | Versioning: semver, v0.x.y until parity sign-off + ≥3 partner uses | Pre-1.0 allows breaking changes between minors. |
| 11 | Changelog: GoReleaser auto-generated from commit messages | Free-form commits accepted; conventional-commit prefixes optional but improve grouping. |
| 12 | Phase 3.5 (personal dogfooding) gates Phase 4 | Maintainer must use the binary on real partner work via `make install-local` before any public release machinery runs. No timeline. |

---

## Progress tracking

Coding agent: tick boxes in this file as work completes. Use
`git commit` per checkbox or per logical grouping; reference the
checkbox ID in the commit message (e.g.,
`feat(release): publish to homebrew tap (4C.2)`). Do not skip phases —
exit criteria gates each one.
