/**
 * Excel parser — extracts partner responses from competency checklists.
 */

import ExcelJS from "exceljs";
import { writeFileSync } from "fs";
import { join } from "path";

const SUFFIX_CONTROLS = [
  "DOC-001", "OPE-001", "OPE-002", "OPE-003",
  "PS-001", "PS-002", "PS-003", "PS-004", "PS-005",
  "REL-001", "REL-002", "USE-CASE",
];

function isCustomerExampleSheet(name) {
  return name.includes("Cust Ex Reqs") || name.includes("Customer Example");
}

/**
 * Find "Partner Response" columns in the sub-header row dynamically.
 */
function findResponseColumns(worksheet, headerRow) {
  const subHeaderRow = headerRow + 1;
  const row = worksheet.getRow(subHeaderRow);
  const cols = [];
  row.eachCell({ includeEmpty: false }, (cell, colNumber) => {
    if (cell.value && String(cell.value).includes("Partner Response")) {
      cols.push(colNumber);
    }
  });
  return cols;
}

/**
 * Extract responses from an Excel file.
 * Returns array of { controlId, partner_response, embedded_link }
 */
export async function extractResponses(xlsxPath, appType = "SOFTWARE") {
  const workbook = new ExcelJS.Workbook();
  await workbook.xlsx.readFile(xlsxPath);

  const responses = [];

  for (const worksheet of workbook.worksheets) {
    const sheetName = worksheet.name;
    if (sheetName.toLowerCase() === "introduction") continue;

    // Find header row (contains "ID")
    let headerRow = null;
    let idCol = null;

    worksheet.eachRow((row, rowNumber) => {
      if (headerRow) return;
      row.eachCell((cell, colNumber) => {
        if (String(cell.value).trim() === "ID") {
          headerRow = rowNumber;
          idCol = colNumber;
        }
      });
    });

    if (!headerRow || !idCol) continue;

    // Find response columns
    let respCols = [];

    if (isCustomerExampleSheet(sheetName)) {
      // Dynamic scan for "Partner Response" in sub-header
      respCols = findResponseColumns(worksheet, headerRow);
      if (respCols.length === 0) {
        // Fallback hardcoded positions
        if (sheetName.includes("GenAI Cust Ex Reqs") || sheetName.includes("Generative AI Customer Example")) {
          respCols = [6, 8, 10, 12]; // F, H, J, L (1-indexed)
        } else {
          respCols = [5, 7, 9, 11]; // E, G, I, K (1-indexed)
        }
      }
    } else {
      // Single-response: scan sub-header and header for "Partner Response"
      respCols = findResponseColumns(worksheet, headerRow);
      if (respCols.length === 0) {
        // Try the header row itself
        const hRow = worksheet.getRow(headerRow);
        hRow.eachCell({ includeEmpty: false }, (cell, colNumber) => {
          if (cell.value && String(cell.value).includes("Partner Response")) {
            respCols.push(colNumber);
          }
        });
      }
    }

    if (respCols.length === 0) continue;

    // Determine start row
    const startRow = isCustomerExampleSheet(sheetName) ? headerRow + 2 : headerRow + 1;

    // Extract responses
    for (let r = startRow; r <= worksheet.rowCount; r++) {
      const row = worksheet.getRow(r);
      const rawId = row.getCell(idCol).value;
      if (!rawId) continue;
      const controlId = String(rawId).trim();
      if (!controlId || controlId === "ID" || controlId === "Partner Response") continue;

      let outId = controlId;
      if (SUFFIX_CONTROLS.includes(controlId)) {
        outId = `${controlId}-${appType}`;
      }

      for (const col of respCols) {
        const cell = row.getCell(col);
        let value = cell.value;
        if (!value) continue;

        // Handle rich text objects (ExcelJS returns { richText: [...] })
        if (typeof value === "object" && value !== null) {
          if (value.richText) {
            value = value.richText.map((r) => r.text || "").join("");
          } else if (value.text) {
            value = value.text;
          } else if (value.result) {
            value = value.result;
          } else {
            value = JSON.stringify(value);
          }
        }

        const text = String(value).trim();
        if (!text || text === "nan" || text === "NaN" || text === "[object Object]") continue;

        // Get hyperlink if present
        const link = cell.hyperlink || "";

        responses.push({
          controlId: outId,
          partner_response: text,
          embedded_link: typeof link === "string" ? link : "",
        });
      }
    }
  }

  return responses;
}

/**
 * Write responses to CSV.
 */
export function writeResponsesCsv(responses, outputPath) {
  const lines = ["controlId,partner_response,embedded_link"];
  for (const r of responses) {
    const resp = r.partner_response.replace(/"/g, '""');
    const link = (r.embedded_link || "").replace(/"/g, '""');
    lines.push(`${r.controlId},"${resp}","${link}"`);
  }
  writeFileSync(outputPath, lines.join("\n"), "utf-8");
  return responses.length;
}
