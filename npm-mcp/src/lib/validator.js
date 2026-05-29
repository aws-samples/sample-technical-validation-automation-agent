/**
 * Core validation engine — validates controls against partner evidence.
 * Features: run manifests, progress logging, DOC-006 marketplace check,
 * waived status tracking, consensus mode.
 */

import { randomUUID } from "crypto";
import { converse } from "./bedrock.js";
import { getSystemPrompt, getContextMap } from "./prompts.js";
import { listEvidenceFiles, processFiles } from "./evidence.js";
import { loadMap, buildMap, getFilesForControl } from "./evidence-map.js";
import { join } from "path";
import { readFileSync, writeFileSync, mkdirSync, existsSync, appendFileSync } from "fs";

const BATCH_SIZE = 4;

/**
 * Write a progress log entry to both stderr and the progress log file.
 */
function logProgress(partnerFolder, message) {
  const logPath = join(partnerFolder, "validation_progress.log");
  const timestamp = new Date().toISOString();
  const entry = `[${timestamp}] ${message}\n`;
  process.stderr.write(entry);
  appendFileSync(logPath, entry, "utf-8");
}

/**
 * DOC-006 marketplace URL check.
 * For control DOC-006, skip Bedrock and do an HTTP HEAD probe on any AWS Marketplace URL.
 * If URL returns 200, PASS. If 404, FAIL. If no URL and response says N/A, WAIVE.
 */
async function checkDoc006Marketplace(controlId, partnerResponse) {
  if (controlId !== "DOC-006") return null;

  // Extract AWS Marketplace URL from partner response
  const urlMatch = partnerResponse.match(/https?:\/\/aws\.amazon\.com\/marketplace\/pp\/[^\s"')]+/i)
    || partnerResponse.match(/https?:\/\/aws\.amazon\.com\/marketplace[^\s"')]+/i);

  if (!urlMatch) {
    // Check if response indicates N/A
    const lower = partnerResponse.toLowerCase();
    if (lower.includes("n/a") || lower.includes("not applicable") || lower.includes("not listed")) {
      return { controlId, status: "WAIVED", reasoning: "Partner response indicates marketplace listing is not applicable." };
    }
    // No URL found, fall through to normal validation
    return null;
  }

  const url = urlMatch[0];
  try {
    const response = await fetch(url, { method: "HEAD", redirect: "follow" });
    if (response.status === 200 || response.status === 301 || response.status === 302) {
      return { controlId, status: "PASSED", reasoning: `AWS Marketplace URL verified (HTTP ${response.status}): ${url}` };
    } else if (response.status === 404) {
      return { controlId, status: "FAILED", reasoning: `AWS Marketplace URL returned 404 (not found): ${url}` };
    } else {
      return { controlId, status: "FAILED", reasoning: `AWS Marketplace URL returned HTTP ${response.status}: ${url}` };
    }
  } catch (err) {
    // Network error — fall through to normal validation
    return null;
  }
}

/**
 * Parse a Bedrock response into a verdict.
 */
function parseResult(controlId, raw) {
  const trimmed = raw.trim();
  if (trimmed.startsWith("YES.")) {
    return { controlId, status: "PASSED", reasoning: trimmed.slice(4).trim() };
  } else if (trimmed.startsWith("NO.")) {
    return { controlId, status: "FAILED", reasoning: trimmed.slice(3).trim() };
  } else if (trimmed.startsWith("WAIVED.")) {
    return { controlId, status: "WAIVED", reasoning: trimmed.slice(7).trim() };
  }
  return { controlId, status: "FAILED", reasoning: trimmed };
}

/**
 * Check if a failure should trigger fallback (evidence mismatch).
 */
function shouldFallback(result) {
  if (result.status !== "FAILED") return false;
  const lower = result.reasoning.toLowerCase();
  const indicators = [
    "different project", "different customer", "different engagement",
    "unrelated document", "unrelated file", "wrong document",
    "document mismatch", "evidence mismatch", "response/docs mismatch",
    "docs mismatch", "docs are about", "documents provided do not",
    "documents do not match", "documents don't match",
    "supporting documents are about", "supporting docs are about",
    "doesn't match what", "don't match what", "does not match what",
    "response describes", "but docs are about", "but documents are about",
  ];
  return indicators.some((ind) => lower.includes(ind));
}

/**
 * Validate a single control.
 */
async function validateControl(controlId, partnerResponse, promptContext, evidenceBlocks) {
  const systemPrompt = getSystemPrompt();
  const contentBlocks = [];

  if (promptContext) contentBlocks.push({ text: promptContext });
  contentBlocks.push({
    text: `Partner Response from self-assessment:\nResponse for ${controlId} submitted by Partner: ${partnerResponse}`,
  });
  contentBlocks.push(...evidenceBlocks);
  contentBlocks.push({ text: "Based on the details provided, is this offering approved?" });

  try {
    const { text } = await converse(systemPrompt, contentBlocks);
    return parseResult(controlId, text);
  } catch (err) {
    return { controlId, status: "ERRORED", reasoning: err.message };
  }
}

/**
 * Validate with fallback — retry failed controls with batched docs.
 */
async function validateWithFallback(controlId, partnerResponse, promptContext, allFiles) {
  for (let i = 0; i < allFiles.length; i += BATCH_SIZE) {
    const batch = allFiles.slice(i, i + BATCH_SIZE);
    const blocks = await processFiles(batch);
    const result = await validateControl(controlId, partnerResponse, promptContext, blocks);
    if (result.status === "PASSED") {
      result.reasoning = `[fallback batch ${Math.floor(i / BATCH_SIZE) + 1}/${Math.ceil(allFiles.length / BATCH_SIZE)}] ${result.reasoning}`;
      return result;
    }
  }
  // All batches exhausted
  return { controlId, status: "FAILED", reasoning: "All evidence batches exhausted without passing" };
}

/**
 * Load partner responses from CSV.
 */
function loadResponses(csvPath) {
  const raw = readFileSync(csvPath, "utf-8");
  const responses = new Map();

  // Proper CSV parsing that handles quoted fields with embedded commas/newlines
  let pos = 0;
  const lines = [];

  // Skip BOM if present
  if (raw.charCodeAt(0) === 0xfeff) pos = 1;

  // Parse header
  const headerEnd = raw.indexOf("\n", pos);
  pos = headerEnd + 1;

  // Parse rows
  while (pos < raw.length) {
    // Read controlId (unquoted, ends at first comma)
    const commaIdx = raw.indexOf(",", pos);
    if (commaIdx === -1) break;
    const controlId = raw.slice(pos, commaIdx).trim();
    pos = commaIdx + 1;

    // Read partner_response (may be quoted with embedded commas/newlines)
    let response = "";
    if (raw[pos] === '"') {
      // Quoted field — find closing quote (handle escaped quotes "")
      pos++; // skip opening quote
      let end = pos;
      while (end < raw.length) {
        if (raw[end] === '"') {
          if (raw[end + 1] === '"') {
            end += 2; // escaped quote
          } else {
            break; // closing quote
          }
        } else {
          end++;
        }
      }
      response = raw.slice(pos, end).replace(/""/g, '"');
      pos = end + 1; // skip closing quote
      // Skip to next line (past comma and any remaining fields)
      const nextLine = raw.indexOf("\n", pos);
      pos = nextLine === -1 ? raw.length : nextLine + 1;
    } else {
      // Unquoted field — ends at newline
      const nextLine = raw.indexOf("\n", pos);
      const lineEnd = nextLine === -1 ? raw.length : nextLine;
      response = raw.slice(pos, lineEnd).trim();
      pos = lineEnd + 1;
    }

    if (!controlId || !response) continue;

    if (!responses.has(controlId)) {
      responses.set(controlId, response);
    } else {
      responses.set(controlId, responses.get(controlId) + "\n\n" + response);
    }
  }

  return responses;
}

/**
 * Run validation on a partner folder.
 * Supports: consensus mode, DOC-006 marketplace check, progress logging, run manifests, waived tracking.
 */
export async function runValidation(partnerFolder, options = {}) {
  const { controls, concurrency = 15, consensus = 1 } = options;
  const csvPath = join(partnerFolder, "partner_responses.csv");
  const supportingDir = join(partnerFolder, "supporting_docs");
  const runId = randomUUID();
  const startedAt = new Date().toISOString();

  if (!existsSync(csvPath)) {
    throw new Error(`partner_responses.csv not found in ${partnerFolder}`);
  }

  // Initialize progress log
  const progressLogPath = join(partnerFolder, "validation_progress.log");
  writeFileSync(progressLogPath, `[${startedAt}] Validation started (run: ${runId})\n`, "utf-8");

  const responses = loadResponses(csvPath);
  const contextMap = getContextMap();
  const allFiles = listEvidenceFiles(supportingDir);

  // Determine which controls to validate
  let controlIds = controls || Array.from(responses.keys());
  controlIds = controlIds.filter((id) => contextMap.has(id));

  logProgress(partnerFolder, `Validating ${controlIds.length} controls (concurrency: ${concurrency}, consensus: ${consensus})`);

  // Build or load evidence map
  const supportingDir2 = join(partnerFolder, "supporting_docs");
  let evidenceMap = loadMap(partnerFolder);
  if (!evidenceMap) {
    evidenceMap = await buildMap(partnerFolder, controlIds, Math.min(concurrency, 6));
  }

  // Phase 1: Validate with evidence-mapped files
  const results = new Map();
  const chunks = [];
  for (let i = 0; i < controlIds.length; i += concurrency) {
    chunks.push(controlIds.slice(i, i + concurrency));
  }

  for (const chunk of chunks) {
    const promises = chunk.map(async (id) => {
      const partnerResp = responses.get(id) || "";
      const promptContext = contextMap.get(id) || "";

      if (!partnerResp) {
        logProgress(partnerFolder, `  ❌ ${id}: FAILED (no partner response)`);
        return { controlId: id, status: "FAILED", reasoning: "No partner response found" };
      }

      // DOC-006 marketplace URL check — skip Bedrock
      const doc006Result = await checkDoc006Marketplace(id, partnerResp);
      if (doc006Result) {
        const icon = doc006Result.status === "PASSED" ? "✅" : doc006Result.status === "WAIVED" ? "⏭️" : "❌";
        logProgress(partnerFolder, `  ${icon} ${id}: ${doc006Result.status} (marketplace check)`);
        return doc006Result;
      }

      // Get mapped files for this control
      const mappedFiles = getFilesForControl(evidenceMap, id, supportingDir2);
      const filesToUse = mappedFiles.length > 0 ? mappedFiles : allFiles.slice(0, BATCH_SIZE);

      try {
        const blocks = await processFiles(filesToUse.slice(0, 5));

        // Consensus mode: run N times and majority-vote
        if (consensus > 1) {
          return await validateWithConsensus(id, partnerResp, promptContext, blocks, consensus, partnerFolder);
        }

        const result = await validateControl(id, partnerResp, promptContext, blocks);
        const icon = result.status === "PASSED" ? "✅" : result.status === "WAIVED" ? "⏭️" : result.status === "ERRORED" ? "⚠️" : "❌";
        logProgress(partnerFolder, `  ${icon} ${id}: ${result.status}`);
        return result;
      } catch (err) {
        logProgress(partnerFolder, `  ⚠️ ${id}: ERRORED (${err.message})`);
        return { controlId: id, status: "ERRORED", reasoning: err.message };
      }
    });

    const chunkResults = await Promise.all(promises);
    for (const r of chunkResults) {
      results.set(r.controlId, r);
    }
  }

  // Phase 2: Fallback for evidence-mismatch failures — batch-until-pass with all files
  const fallbackIds = controlIds.filter((id) => shouldFallback(results.get(id)));
  if (fallbackIds.length > 0) {
    logProgress(partnerFolder, `Fallback: retrying ${fallbackIds.length} controls with batched docs`);
    const fallbackPromises = fallbackIds.map(async (id) => {
      const partnerResp = responses.get(id) || "";
      const promptContext = contextMap.get(id) || "";
      const result = await validateWithFallback(id, partnerResp, promptContext, allFiles);
      results.set(id, result);
      const icon = result.status === "PASSED" ? "✅" : "❌";
      logProgress(partnerFolder, `  ${icon} ${id}: ${result.status} (fallback)`);
    });
    await Promise.all(fallbackPromises);
  }

  // Write summary
  const passed = [...results.values()].filter((r) => r.status === "PASSED").length;
  const failed = [...results.values()].filter((r) => r.status === "FAILED").length;
  const waived = [...results.values()].filter((r) => r.status === "WAIVED").length;
  const errored = [...results.values()].filter((r) => r.status === "ERRORED").length;

  const summary = generateSummary(results, controlIds);
  const summaryPath = join(partnerFolder, "validation_summary.md");
  writeFileSync(summaryPath, summary, "utf-8");

  // Archive
  const reportsDir = join(partnerFolder, "reports", "summary");
  mkdirSync(reportsDir, { recursive: true });
  const ts = new Date().toISOString().replace(/[-:T]/g, "").slice(0, 15);
  writeFileSync(join(reportsDir, `validation_summary_${ts}.md`), summary, "utf-8");

  // Write run manifest
  const completedAt = new Date().toISOString();
  const manifest = {
    id: runId,
    startedAt,
    completedAt,
    modelId: "global.anthropic.claude-sonnet-4-5-20250929-v1:0",
    consensus,
    totalControls: controlIds.length,
    summary: { passed, failed, waived, errored },
    results: controlIds.map((id) => {
      const r = results.get(id);
      return { controlId: id, status: r.status, reasoning: r.reasoning };
    }),
  };

  const manifestDir = join(partnerFolder, "reports");
  mkdirSync(manifestDir, { recursive: true });
  writeFileSync(join(manifestDir, `run_${ts}.json`), JSON.stringify(manifest, null, 2), "utf-8");

  logProgress(partnerFolder, `Validation complete: passed=${passed} failed=${failed} waived=${waived} errored=${errored}`);

  return `Validated ${controlIds.length} controls: passed=${passed} failed=${failed} waived=${waived} errored=${errored}`;
}

/**
 * Consensus mode: run validation N times and majority-vote.
 * Early exit: if consensus=3 and first 2 runs agree, skip run 3.
 */
async function validateWithConsensus(controlId, partnerResponse, promptContext, evidenceBlocks, consensusN, partnerFolder) {
  const votes = [];

  for (let i = 0; i < consensusN; i++) {
    // Early exit: if we already have a majority, skip remaining runs
    if (i >= 2) {
      const passCount = votes.filter((v) => v.status === "PASSED").length;
      const failCount = votes.filter((v) => v.status === "FAILED").length;
      const majority = Math.ceil(consensusN / 2);
      if (passCount >= majority || failCount >= majority) {
        break;
      }
    }

    const result = await validateControl(controlId, partnerResponse, promptContext, evidenceBlocks);
    votes.push(result);
  }

  // Majority vote
  const passCount = votes.filter((v) => v.status === "PASSED").length;
  const failCount = votes.filter((v) => v.status === "FAILED").length;
  const waivedCount = votes.filter((v) => v.status === "WAIVED").length;

  let finalStatus;
  let finalReasoning;

  if (passCount >= failCount && passCount >= waivedCount) {
    finalStatus = "PASSED";
    finalReasoning = votes.find((v) => v.status === "PASSED")?.reasoning || "";
  } else if (waivedCount >= passCount && waivedCount >= failCount) {
    finalStatus = "WAIVED";
    finalReasoning = votes.find((v) => v.status === "WAIVED")?.reasoning || "";
  } else {
    finalStatus = "FAILED";
    finalReasoning = votes.find((v) => v.status === "FAILED")?.reasoning || "";
  }

  finalReasoning = `[consensus ${votes.length}/${consensusN}: ${passCount}P/${failCount}F/${waivedCount}W] ${finalReasoning}`;

  const icon = finalStatus === "PASSED" ? "✅" : finalStatus === "WAIVED" ? "⏭️" : "❌";
  logProgress(partnerFolder, `  ${icon} ${controlId}: ${finalStatus} (consensus ${passCount}P/${failCount}F/${waivedCount}W)`);

  return { controlId, status: finalStatus, reasoning: finalReasoning };
}

function generateSummary(results, controlIds) {
  const passed = [...results.values()].filter((r) => r.status === "PASSED");
  const failed = [...results.values()].filter((r) => r.status === "FAILED");
  const waived = [...results.values()].filter((r) => r.status === "WAIVED");
  const errored = [...results.values()].filter((r) => r.status === "ERRORED");

  let md = `# Validation Summary\n\n`;
  md += `**Timestamp**: ${new Date().toISOString()}\n\n`;
  md += `## Results Overview\n`;
  md += `- **Total Controls**: ${controlIds.length}\n`;
  md += `- **Passed**: ${passed.length} (${((passed.length / controlIds.length) * 100).toFixed(1)}%)\n`;
  md += `- **Failed**: ${failed.length} (${((failed.length / controlIds.length) * 100).toFixed(1)}%)\n`;
  md += `- **Waived**: ${waived.length} (${((waived.length / controlIds.length) * 100).toFixed(1)}%)\n`;
  if (errored.length > 0) {
    md += `- **Errored**: ${errored.length} (${((errored.length / controlIds.length) * 100).toFixed(1)}%)\n`;
  }
  md += `\n`;

  md += `## Passed Controls\n\n`;
  for (const r of passed) {
    md += `### ${r.controlId}\n\n**Reason**:\n\`\`\`\n${r.reasoning}\n\`\`\`\n\n`;
  }

  md += `## Failed Controls\n\n`;
  for (const r of failed) {
    md += `### ${r.controlId}\n\n**Reason**:\n\`\`\`\n${r.reasoning}\n\`\`\`\n\n`;
  }

  if (waived.length > 0) {
    md += `## Waived Controls\n\n`;
    for (const r of waived) {
      md += `### ${r.controlId}\n\n**Reason**:\n\`\`\`\n${r.reasoning}\n\`\`\`\n\n`;
    }
  }

  if (errored.length > 0) {
    md += `## Errored Controls\n\n`;
    for (const r of errored) {
      md += `### ${r.controlId}\n\n**Error**:\n\`\`\`\n${r.reasoning}\n\`\`\`\n\n`;
    }
  }

  md += `---\n*Generated by Thor MCP*\n`;
  return md;
}
