# Setup Workflow

## When to Use

Use this AUTOMATICALLY when:
- The user just installed the Thor power and is using it for the first time
- The Thor MCP server fails to connect
- `thor_doctor` shows any failing checks
- The user mentions setup, configuration, or credential issues

## First-Time Setup (do this proactively)

When a user first activates the Thor power, do the following steps WITHOUT waiting to be asked:

### Step 1: Check if the MCP server is connected

Try calling `thor_doctor`. If it works, skip to Step 4.

If the server is NOT connected (you'll get an error), the user needs to configure the MCP env block. Continue to Step 2.

### Step 2: Configure MCP env block

The user needs to edit `~/.kiro/settings/mcp.json`. Find the `power-thor-power-thor` entry and ensure the `env` block has:

```json
"env": {
  "HOME": "<user-home-directory>",
  "AWS_DEFAULT_REGION": "us-east-1",
  "PATH": "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}
```

To help the user:
1. Detect their home directory (check the workspace path or ask)
2. Open `~/.kiro/settings/mcp.json` and update the env block for them
3. Tell them to reconnect the Thor MCP server (Kiro panel → MCP Servers → reconnect)

**IMPORTANT:** Do NOT add `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, or `AWS_SESSION_TOKEN` to the env block. These expire and prevent auto-refresh. Thor discovers credentials automatically.

**NOTE:** On first server connect, the Python venv and dependencies are installed automatically (~30 seconds). The user may need to reconnect once more after this completes.

### Step 3: AWS Credentials

Ask the user: "How do you get your AWS credentials?" Then guide them:

- **Isengard**: "Run `isengardcli assume <your-account>` in your terminal. Thor will auto-discover the credential_process profile."
- **SSO**: "Run `aws sso login --profile <your-profile>`. If it's not your default profile, I'll add `AWS_PROFILE` to the env block."
- **Ada**: "Run `ada credentials update` in your terminal."
- **Static keys**: "If you have long-lived keys in `~/.aws/credentials` under `[default]`, those will work automatically."

The user does NOT need to tell you which profile — Thor scans `~/.aws/config` and finds `credential_process` profiles automatically.

### Step 4: Verify

Call `thor_doctor`. All 7 checks should pass:
- ✅ Python version
- ✅ AWS Credentials (shows which method was discovered)
- ✅ AWS Region
- ✅ Bedrock client
- ✅ qpdf (optional, ⚠️ is fine)
- ✅ Tools directory
- ✅ Python dependencies

If credentials show as missing, check that `HOME` is set correctly in the env block.

## Common Issues and Fixes

| Symptom | Cause | Fix |
|---------|-------|-----|
| Server won't connect | First run, venv being created | Wait 30 seconds, reconnect |
| `spawn bash ENOENT` | Kiro can't find bash | Change command to `/bin/bash` in mcp.json |
| `AWS Credentials: Missing` | `HOME` not in env block | Add `"HOME": "/Users/theirname"` to env |
| `ExpiredTokenException` | Hardcoded keys in env or stale `~/.aws/credentials` | Remove hardcoded keys, run credential refresh command |
| `qpdf not found` | Not installed | `brew install qpdf` (Mac) — optional, only for large PDFs |
