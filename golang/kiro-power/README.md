# Thor PSA Validator — Kiro Power

Self-contained Kiro Power for Thor: a manifest, an MCP config, and two
steering documents. Install from the Kiro Powers panel pointed at this
directory.

## Files

```
kiro-power/
├── README.md           # this file
├── POWER.md            # Power manifest (registers as thor-psa-validator)
├── mcp.json            # Kiro MCP server config — points at thor-mcp on PATH
└── steering/
    ├── setup.md        # auto-triggered when the Power is first installed
    └── validation.md   # auto-triggered when validating a partner submission
```

## Install

1. Install the `thor-mcp` binary so it's on `PATH`. The fastest path is
   `make install-local` from `golang/`, which copies `thor` and
   `thor-mcp` to `~/.local/bin`. Verify with `thor-mcp --version`.
2. Open the Kiro Powers panel and install **Thor PSA Validator** from
   this directory. Kiro reads `POWER.md` for the manifest and wires the
   MCP server using `mcp.json`.
3. Reconnect the Thor MCP server. Ask Kiro to *"run thor_doctor"* to
   confirm the binary, credentials, and Bedrock access are all
   healthy.

## Working inside this repo

If you're iterating on Thor itself, open the `golang/` directory
directly in Kiro. The `.kiro/steering/` symlinks at
`golang/.kiro/steering/` already point at the canonical files in
`golang/kiro-power/steering/`, so steering loads automatically without
any copying. Edit the file in `kiro-power/steering/` and reload Kiro
to pick up the change.
