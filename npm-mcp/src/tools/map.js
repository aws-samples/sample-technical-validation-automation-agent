/**
 * thor_map — Standalone evidence map builder tool.
 * Exposes the evidence map builder as its own tool (currently embedded in validate).
 */

import { buildMap, loadMap } from "../lib/evidence-map.js";
import { getControlIds, getContextMap } from "../lib/prompts.js";
import { existsSync } from "fs";
import { join } from "path";

/**
 * Build or rebuild the evidence map for a partner folder.
 */
export async function map(partnerFolder, options = {}) {
  const { force = false, concurrency = 6 } = options;

  const supportingDir = join(partnerFolder, "supporting_docs");
  if (!existsSync(supportingDir)) {
    throw new Error(`supporting_docs/ not found in ${partnerFolder}`);
  }

  // Check for existing map
  if (!force) {
    const existing = loadMap(partnerFolder);
    if (existing) {
      const controlCount = Object.keys(existing.controls || {}).length;
      const fileCount = Object.keys(existing.files || {}).length;
      const unmappedCount = (existing.unmapped || []).length;
      return `Evidence map already exists (${controlCount} controls mapped, ${fileCount} files processed, ${unmappedCount} unmapped). Use force=true to rebuild.`;
    }
  }

  // Get all control IDs
  const contextMap = getContextMap();
  const controlIds = getControlIds().filter((id) => contextMap.has(id));

  // Build the map
  const map = await buildMap(partnerFolder, controlIds, concurrency);

  const controlCount = Object.keys(map.controls || {}).length;
  const fileCount = Object.keys(map.files || {}).length;
  const unmappedCount = (map.unmapped || []).length;
  const emptyCount = (map.emptyControls || []).length;

  let result = `Evidence map built successfully:\n`;
  result += `- Controls with evidence: ${controlCount}\n`;
  result += `- Files mapped: ${fileCount}\n`;
  result += `- Unmapped files: ${unmappedCount}\n`;
  result += `- Controls without evidence: ${emptyCount}\n`;

  if (map.emptyControls && map.emptyControls.length > 0) {
    result += `\nControls without mapped evidence:\n`;
    result += map.emptyControls.map((id) => `  - ${id}`).join("\n");
  }

  return result;
}
