# Thor PSA Validator — Claude Code integration

Self-contained Claude Code integration for Thor: an MCP config, a
project context file, and a validation skill — copy what you need into
the project where you'll run Claude Code, or open this directory
directly when iterating on Thor itself.

## Step 1 — Install the binary

You need a `thor-mcp` executable on `PATH`. The bundled `mcp.json`
invokes the binary by name; an absolute path works too.

```sh
cd <repo>
make build
make install-local        # copies bin/thor and bin/thor-mcp to ~/.local/bin
                          # override with THOR_INSTALL_DIR=...
```

If `~/.local/bin` isn't on your `PATH`, the install target prints the
exact line to add to your shell rc.

Verify:

```sh
thor --version
thor-mcp --version
thor doctor
```

## Step 2 — Wire Claude Code to `thor-mcp`

Copy the bundled MCP config into the project where you'll run Claude
Code:

```sh
cp claude-skill/mcp.json /path/to/your/project/.mcp.json
```

The bundled config invokes `thor-mcp` from `PATH` with `us-east-1`
as the default region. It uses the AWS SDK default credential chain
(no profile hardcoded), so it works out of the box if you have valid
credentials via SSO, `credential_process`, or env vars.

If you need a specific profile or region, edit the `env` block:

```json
{
  "mcpServers": {
    "thor": {
      "command": "thor-mcp",
      "args": [],
      "env": {
        "AWS_REGION": "us-east-1",
        "AWS_PROFILE": "your-profile-name"
      }
    }
  }
}
```

## Step 3 — Add project context (optional)

Copy `CLAUDE.md` so Claude always has Thor context loaded for that
project:

```sh
cp claude-skill/CLAUDE.md /path/to/your/project/CLAUDE.md
```

## Step 4 — Install the validation skill (optional)

```sh
mkdir -p /path/to/your/project/.claude/skills
cp -R claude-skill/skills/thor-validate /path/to/your/project/.claude/skills/
```

Claude Code discovers skills as `.claude/skills/<name>/SKILL.md` — the
file MUST be named `SKILL.md` and live inside a directory named for the
skill. When the user mentions validating a partner submission, Claude
Code will auto-invoke this skill.

If you'd rather keep all Thor assets in one place and let Claude Code
read through a symlink, replace the copy above with:

```sh
ln -s /absolute/path/to/golang/claude-skill/skills /path/to/your/project/.claude/skills
```

This is exactly how `golang/.claude/skills` is wired in this repo —
see [Working inside this repo](#working-inside-this-repo) below.

## Step 5 — Verify

In Claude Code, say: *"run thor_doctor"*. You should see all checks
pass: embedded prompts, AWS credentials, Bedrock model access in the
active region.

If credentials are missing or expired, refresh with **whichever
applies** — pick one:

```
aws sso login --profile <profile>     # SSO users
aws configure                         # static keys
isengardcli assume <account>          # Amazon Cloud Desktop
ada credentials update                # alternative for Cloud Desktop
```

No restart needed — the Bedrock client picks up refreshed credentials
on the next call.

## Working inside this repo

If you're iterating on Thor itself, open the repo root in
Claude Code directly. The `.claude/skills` directory is a single
symlink pointing at `claude-skill/skills/`:

```
golang/.claude/skills -> ../claude-skill/skills
```

That means every skill under `claude-skill/skills/<name>/SKILL.md`
shows up automatically — no per-skill symlink, no copying. Edit any
`SKILL.md` in `claude-skill/skills/` and reload Claude Code to pick up
the change.

## Adding a new skill

Skills live canonically under `claude-skill/skills/<name>/SKILL.md`,
matching the layout Claude Code expects (`.claude/skills/<name>/SKILL.md`).
The `.claude/skills` symlink above means dropping a new skill directory
into `claude-skill/skills/` is the only step needed.

From the `golang/` directory:

```sh
SKILL=my-new-skill
mkdir -p claude-skill/skills/$SKILL
$EDITOR claude-skill/skills/$SKILL/SKILL.md
```

`SKILL.md` MUST start with frontmatter — `name` must match the
directory name, and `description` is what Claude scans to decide when
to invoke:

```markdown
---
name: my-new-skill
description: One sentence on when this skill applies. Be specific and
  include the verbs and nouns the user is likely to say. Claude only
  reads the body once it has decided to invoke based on this description.
---

# Skill body — workflow steps, tool call sequences, examples
```

Notes:

- Skills are loaded at session start. Restart Claude Code (or reload the
  skills panel) after creating one.
- Supporting files (templates, scripts, fixtures) can live alongside
  `SKILL.md` inside the skill directory and be referenced by relative
  path from the body.
- A flat `.claude/skills/<name>.md` will NOT load — the file must be
  named `SKILL.md` inside a per-skill directory.

> **Kiro equivalent:** Kiro uses a flatter convention. The whole
> `.kiro/steering` directory in this repo is *not* a single symlink —
> individual files are linked because Kiro reads any `.md` directly
> under `steering/` (no per-skill subdirectory, filename is freeform):
>
> ```
> golang/.kiro/steering/setup.md      -> ../../kiro-power/steering/setup.md
> golang/.kiro/steering/validation.md -> ../../kiro-power/steering/validation.md
> ```
>
> To add a new Kiro steering doc: create
> `kiro-power/steering/<name>.md`, then
> `ln -s ../../kiro-power/steering/<name>.md .kiro/steering/<name>.md`.

## What's in this directory

```
claude-skill/
├── README.md                        # this file
├── mcp.json                         # Claude Code MCP config — copy as .mcp.json
├── CLAUDE.md                        # project context — copy as CLAUDE.md
└── skills/
    └── thor-validate/
        └── SKILL.md                 # validation workflow skill
```

## Troubleshooting

| Error | Fix |
|-------|-----|
| `spawn thor-mcp ENOENT` | `thor-mcp` isn't on `PATH`. Run `make install-local` or replace the `command` in `mcp.json` with an absolute path. |
| `ExpiredTokenException` / "credentials expired" | Run one of the four refresh commands above and retry. No restart needed. |
| `AccessDeniedException` on Converse | Your AWS profile doesn't have Bedrock access in the configured region. Check `aws bedrock list-inference-profiles --region <region>`. |
| Tools not appearing in Claude Code | Make sure `.mcp.json` is in the directory you launch `claude` from, then reload the MCP servers panel. |
