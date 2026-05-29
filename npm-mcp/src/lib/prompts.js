/**
 * Embedded prompts and control catalog loader.
 */

import { readFileSync } from "fs";
import { fileURLToPath } from "url";
import { dirname, join } from "path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const PROMPTS_DIR = join(__dirname, "..", "prompts");

let _systemPrompt = null;
let _contextMap = null;

export function getSystemPrompt() {
  if (!_systemPrompt) {
    _systemPrompt = readFileSync(join(PROMPTS_DIR, "system_revised.txt"), "utf-8");
  }
  return _systemPrompt;
}

export function getContextMap() {
  if (!_contextMap) {
    const raw = readFileSync(join(PROMPTS_DIR, "CONTEXT.csv"), "utf-8");
    _contextMap = new Map();
    // Simple CSV parse: first line is header, then controlId,prompt_context
    const lines = raw.split("\n");
    for (let i = 1; i < lines.length; i++) {
      const line = lines[i];
      if (!line.trim()) continue;
      // Handle quoted CSV fields
      const match = line.match(/^([^,]+),(.+)$/s);
      if (match) {
        const id = match[1].trim();
        let context = match[2].trim();
        // Remove surrounding quotes if present
        if (context.startsWith('"') && context.endsWith('"')) {
          context = context.slice(1, -1).replace(/""/g, '"');
        }
        _contextMap.set(id, context);
      }
    }
  }
  return _contextMap;
}

export function getControlIds() {
  return Array.from(getContextMap().keys());
}
