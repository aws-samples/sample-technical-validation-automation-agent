/**
 * thor_list_controls — list available controls.
 */

import { getControlIds, getContextMap } from "../lib/prompts.js";

export function listControls(category) {
  const ids = getControlIds();
  // TODO: category filtering (for now returns all)
  return `# All Controls (${ids.length})\n${ids.map((id) => `- ${id}`).join("\n")}`;
}
