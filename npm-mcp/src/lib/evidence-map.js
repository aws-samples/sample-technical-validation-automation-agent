/**
 * Evidence mapping — assigns supporting docs to specific controls
 * via a Bedrock pre-pass. Cached in evidence_map.json per partner folder.
 * Replicates the Go version's two-pass mapper with confidence scoring.
 * Includes hash-based invalidation to detect changes in source data.
 */

import { readFileSync, writeFileSync, existsSync, readdirSync } from "fs";
import { join, basename } from "path";
import { createHash } from "crypto";
import { converse } from "./bedrock.js";
import { getControlIds, getContextMap } from "./prompts.js";
import { listEvidenceFiles, processFiles } from "./evidence.js";

const MAP_FILE = "evidence_map.json";
const MIN_CONFIDENCE = 0.3;

/**
 * Compute a hash of partner_responses.csv content + supporting_docs file list.
 * Used for cache invalidation.
 */
function computeEvidenceHash(partnerFolder) {
  const hash = createHash("sha256");

  // Hash the CSV content
  const csvPath = join(partnerFolder, "partner_responses.csv");
  if (existsSync(csvPath)) {
    hash.update(readFileSync(csvPath));
  }

  // Hash the sorted list of supporting doc filenames
  const supportingDir = join(partnerFolder, "supporting_docs");
  if (existsSync(supportingDir)) {
    const files = readdirSync(supportingDir)
      .filter((f) => !f.startsWith("~$") && !f.startsWith("."))
      .sort();
    hash.update(files.join("|"));
  }

  return hash.digest("hex");
}

/**
 * Load cached evidence map if it exists and is still valid (hash matches).
 */
export function loadMap(partnerFolder) {
  const mapPath = join(partnerFolder, MAP_FILE);
  if (!existsSync(mapPath)) return null;
  try {
    const map = JSON.parse(readFileSync(mapPath, "utf-8"));

    // Hash-based invalidation: recompute and compare
    if (map._evidenceHash) {
      const currentHash = computeEvidenceHash(partnerFolder);
      if (currentHash !== map._evidenceHash) {
        process.stderr.write(`[thor] Evidence map invalidated (source files changed). Will rebuild.\n`);
        return null;
      }
    }

    return map;
  } catch {
    return null;
  }
}

function saveMap(partnerFolder, map) {
  // Store the hash for future invalidation checks
  map._evidenceHash = computeEvidenceHash(partnerFolder);
  writeFileSync(join(partnerFolder, MAP_FILE), JSON.stringify(map, null, 2), "utf-8");
}

function buildMapperPrompt(declaredControls, contextMap) {
  let prompt = `You are an evidence-mapping reviewer for the AWS Partner Solution Assessment (PSA) program.

CONTEXT
The AWS Partner Solution Assessment validates that a partner's solution meets AWS competency standards across a fixed set of controls — security, operations, reliability, customer success, and (for AI competencies) responsible-AI controls. Each control has a rubric describing what evidence would satisfy it. A partner submits a self-assessment spreadsheet plus a folder of supporting documents (architecture diagrams, runbooks, customer case studies, security policies, SOWs, marketing decks, screenshots, etc.).

YOUR TASK
You will be shown the contents of ONE supporting document. Identify every control from the catalog below for which this document is reasonable evidence. Score each match with a confidence between 0.0 and 1.0.

DECISION RULES
1. A document is "evidence" for a control when its content addresses the topic the control's rubric evaluates — even partially. Err on inclusion when content overlaps the rubric.
2. Marketing decks, capability summaries, and case studies often map to multiple use-case, customer-example, and capability controls. Treat them generously.
3. Architecture diagrams typically map to documentation, networking-security, reliability, scalability, and operations controls.
4. Production handbooks / runbooks / SOPs map to operational excellence, incident response, change management, and reliability controls.
5. Security policies / IAM diagrams / threat models map to security, identity, and governance controls.
6. SOW templates, project-management artifacts, and onboarding guides map to project-services controls (PRJ, POV, PS).
7. Foundation-model / training-data / responsible-AI documentation maps to GAI* / QCHK* / AGAI* controls if present in the catalog.
8. ONLY use control IDs from the catalog below. Do NOT invent IDs, drop suffixes, or shorten names.
9. If the document is genuinely off-topic for every catalog entry, return an empty assignments array.

CONFIDENCE SCALE
- 0.9-1.0: rubric is the document's primary subject; this is a textbook fit.
- 0.6-0.8: rubric is one of several topics the document addresses; clearly relevant.
- 0.3-0.5: document mentions the topic in passing or covers an adjacent area.
- below 0.3: do not include — the match is too weak.

OUTPUT FORMAT (strict JSON, no prose, no markdown fences):
{
  "assignments": [
    {"controlId": "<exact ID from catalog>", "confidence": <0.0-1.0>}
  ],
  "rationale": "one or two sentences naming the topics the document covers and why those topics matched the listed controls"
}

CATALOG OF DECLARED CONTROLS (controlId | rubric):
`;

  for (const id of declaredControls) {
    const ctx = (contextMap.get(id) || "").replace(/\n/g, " ").slice(0, 150);
    prompt += `${id} | ${ctx}\n`;
  }

  return prompt;
}

/**
 * Build the evidence map by asking Bedrock which controls each file supports.
 */
export async function buildMap(partnerFolder, declaredControls, concurrency = 6) {
  const supportingDir = join(partnerFolder, "supporting_docs");
  const files = listEvidenceFiles(supportingDir);

  if (files.length === 0) return { controls: {}, files: {}, unmapped: [], emptyControls: declaredControls };

  const contextMap = getContextMap();
  const controlList = declaredControls.filter((id) => contextMap.has(id));
  const systemPrompt = buildMapperPrompt(controlList, contextMap);

  const map = { controls: {}, files: {}, unmapped: [], emptyControls: [] };

  process.stderr.write(`[thor] Building evidence map: ${files.length} files, ${controlList.length} controls\n`);

  // Process files with bounded concurrency
  const chunks = [];
  for (let i = 0; i < files.length; i += concurrency) {
    chunks.push(files.slice(i, i + concurrency));
  }

  for (const chunk of chunks) {
    const promises = chunk.map(async (filePath) => {
      const fileName = basename(filePath);
      try {
        const blocks = await processFiles([filePath]);
        if (blocks.length === 0) return { fileName, assignments: [] };

        const userContent = [
          { text: "Analyze this document and map it to the controls in the catalog." },
          ...blocks,
        ];

        const { text } = await converse(systemPrompt, userContent);

        // Parse JSON response
        const jsonMatch = text.match(/\{[\s\S]*\}/);
        if (!jsonMatch) return { fileName, assignments: [] };

        const parsed = JSON.parse(jsonMatch[0]);
        const assignments = (parsed.assignments || [])
          .filter((a) => a.confidence >= MIN_CONFIDENCE && controlList.includes(a.controlId))
          .sort((a, b) => b.confidence - a.confidence);

        return { fileName, assignments };
      } catch (err) {
        process.stderr.write(`  ⚠️ Map failed for ${fileName}: ${err.message}\n`);
        return { fileName, assignments: [] };
      }
    });

    const results = await Promise.all(promises);
    for (const { fileName, assignments } of results) {
      if (assignments.length === 0) {
        map.unmapped.push(fileName);
      } else {
        map.files[fileName] = assignments.map((a) => a.controlId);
        for (const a of assignments) {
          if (!map.controls[a.controlId]) map.controls[a.controlId] = [];
          if (!map.controls[a.controlId].includes(fileName)) {
            map.controls[a.controlId].push(fileName);
          }
        }
      }
      process.stderr.write(`  📄 ${fileName} → ${assignments.length > 0 ? assignments.map((a) => a.controlId).join(", ") : "(unmapped)"}\n`);
    }
  }

  map.emptyControls = controlList.filter((id) => !map.controls[id] || map.controls[id].length === 0);

  saveMap(partnerFolder, map);
  process.stderr.write(`[thor] Evidence map complete: ${Object.keys(map.controls).length} controls mapped, ${map.unmapped.length} unmapped, ${map.emptyControls.length} empty\n`);

  return map;
}

/**
 * Get the files mapped to a specific control. Returns full paths.
 */
export function getFilesForControl(map, controlId, supportingDir) {
  const fileNames = map?.controls?.[controlId] || [];
  return fileNames.map((f) => join(supportingDir, f));
}
