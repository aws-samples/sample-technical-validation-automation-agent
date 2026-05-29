/**
 * thor_prep — create partner folder structure.
 */

import { mkdirSync, existsSync } from "fs";
import { join } from "path";
import { homedir } from "os";

export async function prep(partnerName, category, baseFolder) {
  const base = baseFolder || join(homedir(), "Documents", "PSA_Validations");
  const folder = join(base, partnerName);

  if (existsSync(folder)) {
    return `Partner folder already exists: ${folder}`;
  }

  mkdirSync(join(folder, "supporting_docs"), { recursive: true });
  mkdirSync(join(folder, "reports", "summary"), { recursive: true });

  let msg = `Created partner folder: ${folder}\n`;
  msg += `\nNext steps:\n`;
  msg += `1. Place the Excel checklist (.xlsx) in ${folder}/\n`;
  msg += `2. Place supporting documents in ${folder}/supporting_docs/\n`;
  msg += `3. Run thor_convert to extract responses\n`;
  msg += `4. Run thor_validate to validate all controls`;

  return msg;
}
