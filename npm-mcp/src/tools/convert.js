/**
 * thor_convert — Excel to CSV conversion.
 */

import { readdirSync, statSync } from "fs";
import { join, extname } from "path";
import { extractResponses, writeResponsesCsv } from "../lib/excel.js";

/**
 * Find the most recent .xlsx file in a folder.
 */
function findExcelFile(folder) {
  const files = readdirSync(folder)
    .filter((f) => (extname(f) === ".xlsx" || extname(f) === ".xls") && !f.startsWith("~$"))
    .map((f) => ({ name: f, path: join(folder, f), mtime: statSync(join(folder, f)).mtimeMs }))
    .sort((a, b) => b.mtime - a.mtime);

  if (files.length === 0) throw new Error(`No .xlsx file found in ${folder}`);
  return files[0].path;
}

export async function convert(partnerFolder, appType = "SOFTWARE") {
  const xlsxPath = findExcelFile(partnerFolder);
  const outputPath = join(partnerFolder, "partner_responses.csv");

  const responses = await extractResponses(xlsxPath, appType);
  const count = writeResponsesCsv(responses, outputPath);

  return `Extracted ${count} responses from ${xlsxPath} -> ${outputPath}`;
}
