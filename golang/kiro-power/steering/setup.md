# Setup Workflow

## When to Use

Use this AUTOMATICALLY when:
- The user just installed the Thor (Go) Power and is using it for the first time
- The Thor MCP server fails to connect
- `thor_doctor` shows any failing checks
- The user mentions setup, configuration, binary install, or credential issues

## First-Time Setup (do this proactively)

When a user first activates the Thor Go Power, do the following steps
WITHOUT waiting to be asked:

### Step 1: Check if the MCP server is connected

Try calling `thor_doctor`. If it returns a structured report, skip to
Step 4.

If the server is NOT connected (you'll get an error), the user is most
likely missing the `thor-mcp` binary on `PATH`. Continue to Step 2.

### Step 2: Verify the binary is installed

Ask the user to run in their terminal:

```sh
which thor-mcp
thor-mcp --version
```

If `which thor-mcp` returns nothing:

- **Maintainer / contributor build** — guide them to:
  ```sh
  cd <repo>/golang
  make install-local
  ```
  Then have them confirm `~/.local/bin` is on their `PATH` (the install
  target prints the exact rc-file line if not).
- **Homebrew (macOS / Linux, post-Phase 4)** — `brew tap aws-samples/thor && brew install thor`
- **Scoop (Windows, post-Phase 4)** — `scoop bucket add thor https://github.com/aws-samples/scoop-thor && scoop install thor`
- **Direct download** — grab the archive from GitHub Releases, extract,
  put `thor` and `thor-mcp` in a directory that's on `PATH`.

### Step 3: Configure MCP env block

The user needs to verify `~/.kiro/settings/mcp.json`. Find the
`power-thor-psa-validator-go-thor` entry (Kiro creates it on Power
install) and confirm the `env` block. The default the Power ships with
is:

```json
"env": {
  "AWS_REGION": "us-east-1",
  "PATH": "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${env:HOME}/.local/bin:${env:HOME}/go/bin"
}
```

That `PATH` covers Homebrew (`/opt/homebrew/bin`), system bins,
`make install-local` output (`~/.local/bin`), and `go install` output
(`~/go/bin`). If `thor-mcp` is somewhere else, set the `command` field
to its absolute path instead.

**IMPORTANT:** Do NOT add `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
or `AWS_SESSION_TOKEN` to the env block. These expire and prevent
auto-refresh. The Go binary uses the AWS SDK default chain, which
discovers credentials automatically.

After editing, tell them to reconnect the Thor MCP server (Kiro panel →
MCP Servers → reconnect).

### Step 4: AWS Credentials

Ask the user: *"How do you get your AWS credentials?"* Then tell them to
run **whichever applies** — they only need one:

```
aws sso login --profile <profile>     # SSO users
aws configure                         # static keys
isengardcli assume <account>          # Amazon Cloud Desktop
ada credentials update                # alternative for Cloud Desktop
```

The user does NOT need to tell you which profile — the SDK scans
`~/.aws/config` for `credential_process` and SSO profiles
automatically. If they want a non-default profile, they can set
`AWS_PROFILE` in the `env` block.

### Step 5: Verify

Call `thor_doctor`. The structured report should show:

- Embedded prompts: `system_old.txt`, `system_new.txt`,
  `system_revised.txt`, and `CONTEXT.csv` all present.
- AWS configuration: region, profile, credential source resolved.
- STS caller identity: account + ARN.
- Bedrock: at least one inference profile with id starting
  `global.anthropic.claude-sonnet-4-5`.

If credentials show as missing, double-check the env block (see Step 3)
and that the user actually ran a credential command in Step 4.

## Common Issues and Fixes

| Symptom | Cause | Fix |
|---------|-------|-----|
| `spawn thor-mcp ENOENT` | Binary not on `PATH` | Run `make install-local` or set `command` to an absolute path in `mcp.json` |
| Connect succeeds but `thor_doctor` says embedded prompts missing | Binary built incorrectly (rare; only possible from a tampered source tree) | Rebuild: `make clean && make build && make install-local` |
| `AWS Credentials: Missing` | Default chain found nothing | Run one of the four refresh commands above; do not add static keys to `env` |
| `ExpiredTokenException` | Credentials timed out | Re-run the refresh command and retry — no server restart needed |
| `AccessDeniedException` on Converse | Profile lacks Bedrock access in the active region | `aws bedrock list-inference-profiles --region <region>` to verify, or pick a different profile via `AWS_PROFILE` |

## Versus the Python build

If the user already has the Python Power installed (`thor-psa-validator`),
the Go Power (`thor-psa-validator-go`) installs alongside it without
collision. They're free to use either or both — same partner folder
structure, same `validation_summary.md` format. The Go build starts
instantly and needs no Python venv.
