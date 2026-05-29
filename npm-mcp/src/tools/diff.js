/**
 * thor_diff — Compare validation runs.
 * Modes: "latest" (compare 2 most recent), "all" (timeline table), "custom" (pick two by timestamp).
 */

import { readdirSync, readFileSync } from "fs";
import { join } from "path";

/**
 * Parse a validation summary markdown into structured data.
 */
function parseSummary(content, filename) {
  const timestamp = filename.match(/validation_summary_(\d{8}_?\d{6})\.md/)?.[1] || filename;
  const controls = [];

  const passedSection = content.match(/## Passed Controls\s*\n([\s\S]*?)(?=## Failed|## Waived|---|\n$)/);
  const failedSection = content.match(/## Failed Controls\s*\n([\s\S]*?)(?=## Passed|## Waived|---|\n$)/);
  const waivedSection = content.match(/## Waived Controls\s*\n([\s\S]*?)(?=## Passed|## Failed|---|\n$)/);

  if (passedSection) {
    const ids = passedSection[1].match(/### ([\w-]+)/g) || [];
    ids.forEach((m) => controls.push({ id: m.replace("### ", ""), status: "PASSED" }));
  }
  if (failedSection) {
    const ids = failedSection[1].match(/### ([\w-]+)/g) || [];
    ids.forEach((m) => controls.push({ id: m.replace("### ", ""), status: "FAILED" }));
  }
  if (waivedSection) {
    const ids = waivedSection[1].match(/### ([\w-]+)/g) || [];
    ids.forEach((m) => controls.push({ id: m.replace("### ", ""), status: "WAIVED" }));
  }

  return { timestamp, controls, filename };
}

/**
 * List all summary files sorted by timestamp (newest first).
 */
function listSummaries(partnerFolder) {
  const reportsDir = join(partnerFolder, "reports", "summary");
  try {
    const files = readdirSync(reportsDir)
      .filter((f) => f.startsWith("validation_summary_") && f.endsWith(".md"))
      .sort()
      .reverse();
    return files.map((f) => ({
      filename: f,
      path: join(reportsDir, f),
      ...parseSummary(readFileSync(join(reportsDir, f), "utf-8"), f),
    }));
  } catch {
    return [];
  }
}

/**
 * Compare two runs side by side.
 */
function compareTwoRuns(runA, runB) {
  const allIds = new Set([
    ...runA.controls.map((c) => c.id),
    ...runB.controls.map((c) => c.id),
  ]);

  let md = `# Validation Diff\n\n`;
  md += `| Run A | Run B |\n|-------|-------|\n`;
  md += `| ${runA.timestamp} | ${runB.timestamp} |\n\n`;

  const changes = [];
  const unchanged = [];

  for (const id of [...allIds].sort()) {
    const statusA = runA.controls.find((c) => c.id === id)?.status || "—";
    const statusB = runB.controls.find((c) => c.id === id)?.status || "—";
    if (statusA !== statusB) {
      changes.push({ id, statusA, statusB });
    } else {
      unchanged.push({ id, status: statusA });
    }
  }

  md += `## Changes (${changes.length})\n\n`;
  if (changes.length > 0) {
    md += `| Control | Run A | Run B | Delta |\n|---------|-------|-------|-------|\n`;
    for (const c of changes) {
      const delta = c.statusB === "PASSED" ? "🟢 improved" : c.statusA === "PASSED" ? "🔴 regressed" : "🔄 changed";
      md += `| ${c.id} | ${c.statusA} | ${c.statusB} | ${delta} |\n`;
    }
  } else {
    md += `No changes between runs.\n`;
  }

  md += `\n## Unchanged (${unchanged.length})\n\n`;
  const passedCount = unchanged.filter((u) => u.status === "PASSED").length;
  const failedCount = unchanged.filter((u) => u.status === "FAILED").length;
  const waivedCount = unchanged.filter((u) => u.status === "WAIVED").length;
  md += `- Passed: ${passedCount}\n- Failed: ${failedCount}\n- Waived: ${waivedCount}\n`;

  return md;
}

/**
 * Timeline table of all runs.
 */
function timelineTable(summaries) {
  if (summaries.length === 0) return "No validation runs found.";

  const allIds = new Set();
  for (const s of summaries) {
    for (const c of s.controls) allIds.add(c.id);
  }
  const sortedIds = [...allIds].sort();
  const sortedRuns = [...summaries].reverse(); // oldest first

  let md = `# Validation Timeline (${sortedRuns.length} runs)\n\n`;
  md += `| Control | ${sortedRuns.map((r) => r.timestamp).join(" | ")} |\n`;
  md += `|---------|${sortedRuns.map(() => "-------").join("|")}|\n`;

  for (const id of sortedIds) {
    const row = sortedRuns.map((r) => {
      const ctrl = r.controls.find((c) => c.id === id);
      if (!ctrl) return "—";
      return ctrl.status === "PASSED" ? "✅" : ctrl.status === "WAIVED" ? "⏭️" : "❌";
    });
    md += `| ${id} | ${row.join(" | ")} |\n`;
  }

  return md;
}

/**
 * Main diff entry point.
 */
export function diff(partnerFolder, mode = "latest", timestampA, timestampB) {
  const summaries = listSummaries(partnerFolder);

  if (summaries.length === 0) {
    return "No validation summaries found in reports/summary/. Run thor_validate first.";
  }

  switch (mode) {
    case "latest": {
      if (summaries.length < 2) {
        return "Need at least 2 validation runs to compare. Only found 1.";
      }
      return compareTwoRuns(summaries[1], summaries[0]);
    }

    case "all": {
      return timelineTable(summaries);
    }

    case "custom": {
      if (!timestampA || !timestampB) {
        const available = summaries.map((s) => s.timestamp).join(", ");
        return `Please provide two timestamps. Available: ${available}`;
      }
      const runA = summaries.find((s) => s.timestamp.includes(timestampA));
      const runB = summaries.find((s) => s.timestamp.includes(timestampB));
      if (!runA) return `Run not found for timestamp: ${timestampA}`;
      if (!runB) return `Run not found for timestamp: ${timestampB}`;
      return compareTwoRuns(runA, runB);
    }

    default:
      return `Unknown mode: ${mode}. Use "latest", "all", or "custom".`;
  }
}
