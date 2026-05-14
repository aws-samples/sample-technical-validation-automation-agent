# Thor PSA Validator — Kiro Power (Go build)

Self-contained Kiro Power for the Go port of Thor. Drop-in alternative
to the Python `POWER.md` at the repo root — install side-by-side or pick
just one.

> The Python build is at the repo root and is unaffected by anything
> here. See the root `POWER.md` for the Python alternative.

## Files

```
kiro-power/
├── README.md           # this file
├── POWER.md            # Power manifest (registers as thor-psa-validator-go)
├── mcp.json            # Kiro MCP server config — points at thor-mcp on PATH
└── steering/
    ├── setup.md        # auto-triggered when the Power is first installed
    └── validation.md   # auto-triggered when validating a partner submission
```

## Install

1. Install the `thor-mcp` binary so it's on `PATH`. See `POWER.md` for
   the install paths (`make install-local`, Homebrew, Scoop, or direct
   download). Verify with `thor-mcp --version`.
2. Open the Kiro Powers panel and install **Thor PSA Validator (Go)**
   from this directory. Kiro reads `POWER.md` for the manifest and
   wires the MCP server using `mcp.json`.
3. Reconnect the Thor MCP server. Ask Kiro to *"run thor_doctor"* to
   confirm the binary, credentials, and Bedrock access are all healthy.

The Power's `name` is `thor-psa-validator-go`, so it can live alongside
the Python Power (`thor-psa-validator`) without collision.

## Working inside this repo

If you're iterating on Thor itself, open the `golang/` directory
directly in Kiro. The `.kiro/steering/` symlinks at
`golang/.kiro/steering/` already point at the canonical files in
`golang/kiro-power/steering/`, so steering loads automatically without
any copying. Edit the file in `kiro-power/steering/` and reload Kiro to
pick up the change.

## Versus the Python Power

| | Python Power (root) | Go Power (this dir) |
|---|---|---|
| Manifest name | `thor-psa-validator` | `thor-psa-validator-go` |
| Server | Python venv + bootstrap script | Single `thor-mcp` binary on `PATH` |
| First-run cost | ~30s venv creation | none — instant |
| External deps | `qpdf` for large PDFs | none (`pdfcpu` is in-process) |
| `thor_run` tool | yes (broken — depends on a non-existent module) | dropped |
| Tools shipped | 8 | 7 |
