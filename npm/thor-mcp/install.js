#!/usr/bin/env node
"use strict";

// Postinstall: finds the platform-specific binary from the optional
// dependency and symlinks/copies it to ./bin/thor-mcp so the package's
// "bin" field resolves to the native Go binary directly.

const fs = require("fs");
const path = require("path");

const PLATFORMS = {
  "darwin-arm64": "@asp-sail/thor-mcp-darwin-arm64",
  "darwin-x64": "@asp-sail/thor-mcp-darwin-x64",
  "linux-x64": "@asp-sail/thor-mcp-linux-x64",
  "linux-arm64": "@asp-sail/thor-mcp-linux-arm64",
  "win32-x64": "@asp-sail/thor-mcp-win32-x64",
};

const platform = process.platform;
const arch = process.arch === "arm64" ? "arm64" : "x64";
const key = `${platform}-${arch}`;
const pkg = PLATFORMS[key];

if (!pkg) {
  console.error(`[thor-mcp] No pre-built binary for ${key}. Build from source: make build && make install-local`);
  process.exit(0); // Don't fail install — just warn
}

const binName = platform === "win32" ? "thor-mcp.exe" : "thor-mcp";
const destDir = path.join(__dirname, "bin");
const destPath = path.join(destDir, binName);

// Find the platform package binary
function findBinary() {
  // Strategy 1: require.resolve
  try {
    const pkgDir = path.dirname(require.resolve(`${pkg}/package.json`));
    const src = path.join(pkgDir, "bin", binName);
    if (fs.existsSync(src)) return src;
  } catch (e) {}

  // Strategy 2: Walk up node_modules
  let dir = __dirname;
  for (let i = 0; i < 5; i++) {
    dir = path.dirname(dir);
    const candidate = path.join(dir, "node_modules", pkg, "bin", binName);
    if (fs.existsSync(candidate)) return candidate;
  }

  return null;
}

const src = findBinary();
if (!src) {
  console.warn(`[thor-mcp] Platform binary not found. Install may work on retry or build from source.`);
  process.exit(0);
}

// Ensure bin directory exists
if (!fs.existsSync(destDir)) {
  fs.mkdirSync(destDir, { recursive: true });
}

// Copy binary to bin/
fs.copyFileSync(src, destPath);
fs.chmodSync(destPath, 0o755);
console.log(`[thor-mcp] Installed ${key} binary to ${destPath}`);
