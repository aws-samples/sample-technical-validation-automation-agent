# Setup Workflow

## When to Use

Use this AUTOMATICALLY when:
- The user just installed the power and is using it for the first time
- The MCP server fails to connect
- `thor_doctor` shows any failing checks
- The user mentions setup, configuration, or credential issues

## First-Time Setup (do this proactively)

When a user first activates the power, do the following steps
WITHOUT waiting to be asked:

### Step 1: Check if the MCP server is connected

Try calling `thor_doctor`. If it returns a structured report, skip to
Step 4.

If the server is NOT connected (you'll get an error), continue to
Step 2.

### Step 2: Fix `npx` not found (ENOENT)

If you see `spawn npx ENOENT`, the MCP process can't find `npx`
because Kiro doesn't inherit the user's shell PATH.

Ask the user to run `which npx` in their terminal, then update the
`command` field in `~/.kiro/settings/mcp.json` to the absolute path:

```json
"command": "/absolute/path/to/npx"
```

### Step 3: Configure credentials in the MCP env block

**IMPORTANT:** Kiro MCP processes do NOT inherit the user's shell
environment. They cannot read `AWS_PROFILE`, shell functions, SSO
sessions, or `credential_process` entries. The ONLY reliable method
is to paste credentials directly into the `env` block.

Tell the user to:

1. Get fresh credentials from their identity provider:
   ```sh
   aws configure export-credentials --format env
   ```

2. Open `~/.kiro/settings/mcp.json` and find the
   `power-thor-power-thor` entry.

3. Add the credentials to the `env` block:
   ```json
   "env": {
     "AWS_REGION": "us-east-1",
     "AWS_ACCESS_KEY_ID": "<paste here>",
     "AWS_SECRET_ACCESS_KEY": "<paste here>",
     "AWS_SESSION_TOKEN": "<paste here>",
     "PATH": "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
   }
   ```

4. Save the file. Kiro re-reads it on save and reconnects automatically.

### Step 4: Verify

Call `thor_doctor`. The structured report should show:

- `embeddedPrompts.OK: true` — validation logic loaded
- `aws.Identity` — account + ARN resolved
- `aws.Bedrock.HasModelMatch: true` — Claude model available
- `errors: []` — no errors

If credentials show as expired or missing, have the user refresh them
(get new values) and update the three `AWS_*` fields in the env block.

### Step 5: Refresh expired credentials

When `thor_doctor` returns "security token expired", tell the user:

> Your AWS credentials have expired. Run
> `aws configure export-credentials --format env` to get fresh ones,
> then update the `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and
> `AWS_SESSION_TOKEN` values in your MCP config
> (`~/.kiro/settings/mcp.json`) and save. It reconnects automatically.

## Common Issues and Fixes

| Symptom | Cause | Fix |
|---------|-------|-----|
| `spawn npx ENOENT` | Kiro can't find `npx` | Replace `"command": "npx"` with the absolute path (user runs `which npx`) |
| `Could not load credentials from any providers` | No credentials in env block | Paste `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` into the MCP config env block |
| `ExpiredTokenException` / "credentials expired" | Tokens timed out | Get fresh credentials, update the three `AWS_*` values, save |
| `AccessDeniedException` on Converse | Credentials lack Bedrock access in the region | Verify with `aws bedrock list-inference-profiles --region us-east-1` |
| `thor_doctor` shows prompts missing | Package issue | Try `npx -y @asp-sail/thor-mcp@latest` to pull the latest version |
