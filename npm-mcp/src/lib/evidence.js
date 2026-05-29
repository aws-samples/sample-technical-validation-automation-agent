/**
 * Evidence file processing — reads supporting docs and prepares
 * content blocks for Bedrock Converse.
 * Includes PDF splitting (>40 pages) and image resizing (>8000px).
 */

import { readFileSync, readdirSync, statSync } from "fs";
import { join, extname, basename } from "path";
import pdfParse from "pdf-parse";
import mammoth from "mammoth";
import JSZip from "jszip";
import sharp from "sharp";

const MAX_DOC_BYTES = 4_500_000;
const MAX_IMAGE_DIM = 8000;
const PDF_PAGE_CHUNK_SIZE = 40;

const DOC_FORMATS = new Set([".pdf", ".xlsx", ".xls", ".docx", ".doc", ".csv", ".html", ".txt", ".md"]);
const IMG_FORMATS = new Set([".png", ".jpg", ".jpeg"]);

/**
 * List all evidence files in supporting_docs/.
 */
export function listEvidenceFiles(supportingDocsDir) {
  try {
    const files = readdirSync(supportingDocsDir);
    return files
      .filter((f) => !f.startsWith("~$") && !f.startsWith("."))
      .map((f) => join(supportingDocsDir, f))
      .filter((f) => statSync(f).isFile());
  } catch {
    return [];
  }
}

/**
 * Process files into Bedrock content blocks.
 * Handles image resizing (>8000px) and PDF splitting (>40 pages).
 */
export async function processFiles(filePaths) {
  const blocks = [];

  for (const filePath of filePaths) {
    const ext = extname(filePath).toLowerCase();
    const bytes = readFileSync(filePath);

    if (IMG_FORMATS.has(ext)) {
      const format = ext === ".jpg" ? "jpeg" : ext.slice(1);
      // Check image dimensions and resize if needed
      const resizedBytes = await resizeImageIfNeeded(bytes);
      blocks.push({
        image: { format, source: { bytes: resizedBytes } },
      });
    } else if (ext === ".pptx") {
      // Always text-extract PPTX
      const text = await extractPptxText(bytes);
      if (text) {
        blocks.push({
          document: {
            format: "txt",
            name: basename(filePath).replace(/[^a-zA-Z0-9]/g, "_"),
            source: { bytes: Buffer.from(text.slice(0, MAX_DOC_BYTES)) },
          },
        });
      }
    } else if (ext === ".pdf") {
      // PDF splitting for large documents
      const pdfBlocks = await processPdf(filePath, bytes);
      blocks.push(...pdfBlocks);
    } else if (DOC_FORMATS.has(ext)) {
      if (bytes.length > MAX_DOC_BYTES) {
        // Text extraction fallback for oversized files
        const text = await extractText(filePath, ext, bytes);
        if (text) {
          blocks.push({
            document: {
              format: "txt",
              name: basename(filePath).replace(/[^a-zA-Z0-9]/g, "_"),
              source: { bytes: Buffer.from(text.slice(0, MAX_DOC_BYTES)) },
            },
          });
        }
      } else {
        const format = ext.slice(1); // remove dot
        blocks.push({
          document: {
            format,
            name: basename(filePath).replace(/[^a-zA-Z0-9]/g, "_"),
            source: { bytes },
          },
        });
      }
    }
  }

  return blocks;
}

/**
 * Resize image if any dimension exceeds MAX_IMAGE_DIM (8000px).
 */
async function resizeImageIfNeeded(bytes) {
  try {
    const metadata = await sharp(bytes).metadata();
    const { width, height } = metadata;

    if (width > MAX_IMAGE_DIM || height > MAX_IMAGE_DIM) {
      const scale = Math.min(MAX_IMAGE_DIM / width, MAX_IMAGE_DIM / height);
      const newWidth = Math.round(width * scale);
      const newHeight = Math.round(height * scale);

      process.stderr.write(`  📐 Resizing image from ${width}x${height} to ${newWidth}x${newHeight}\n`);

      const resized = await sharp(bytes)
        .resize(newWidth, newHeight, { fit: "inside" })
        .toBuffer();
      return resized;
    }
  } catch (err) {
    process.stderr.write(`  ⚠️ Image resize check failed: ${err.message}\n`);
  }
  return bytes;
}

/**
 * Process a PDF file. If >40 pages, split into text chunks.
 */
async function processPdf(filePath, bytes) {
  const blocks = [];
  const name = basename(filePath).replace(/[^a-zA-Z0-9]/g, "_");

  try {
    const pdf = await pdfParse(bytes);
    const pageCount = pdf.numpages || 1;

    if (pageCount > PDF_PAGE_CHUNK_SIZE) {
      // Split into chunks by extracting text in page ranges
      process.stderr.write(`  📄 PDF ${basename(filePath)} has ${pageCount} pages, splitting into chunks of ${PDF_PAGE_CHUNK_SIZE}\n`);

      const fullText = pdf.text || "";
      // Approximate page splitting by dividing text evenly
      const charsPerPage = Math.ceil(fullText.length / pageCount);

      for (let startPage = 0; startPage < pageCount; startPage += PDF_PAGE_CHUNK_SIZE) {
        const endPage = Math.min(startPage + PDF_PAGE_CHUNK_SIZE, pageCount);
        const startChar = startPage * charsPerPage;
        const endChar = Math.min(endPage * charsPerPage, fullText.length);
        const chunkText = fullText.slice(startChar, endChar);

        if (chunkText.trim()) {
          blocks.push({
            document: {
              format: "txt",
              name: `${name}_pages_${startPage + 1}_to_${endPage}`,
              source: { bytes: Buffer.from(chunkText.slice(0, MAX_DOC_BYTES)) },
            },
          });
        }
      }
    } else if (bytes.length > MAX_DOC_BYTES) {
      // Small page count but large file — extract text
      const text = pdf.text || "";
      if (text) {
        blocks.push({
          document: {
            format: "txt",
            name,
            source: { bytes: Buffer.from(text.slice(0, MAX_DOC_BYTES)) },
          },
        });
      }
    } else {
      // Small PDF — send as-is
      blocks.push({
        document: {
          format: "pdf",
          name,
          source: { bytes },
        },
      });
    }
  } catch (err) {
    // Fallback: send raw if small enough
    if (bytes.length <= MAX_DOC_BYTES) {
      blocks.push({
        document: {
          format: "pdf",
          name,
          source: { bytes },
        },
      });
    }
  }

  return blocks;
}

async function extractText(filePath, ext, bytes) {
  try {
    switch (ext) {
      case ".pdf":
        const pdf = await pdfParse(bytes);
        return pdf.text;
      case ".docx":
        const result = await mammoth.extractRawText({ buffer: bytes });
        return result.value;
      case ".txt":
      case ".md":
      case ".csv":
      case ".html":
        return bytes.toString("utf-8");
      default:
        return null;
    }
  } catch {
    return null;
  }
}

async function extractPptxText(bytes) {
  try {
    const zip = await JSZip.loadAsync(bytes);
    const texts = [];
    const slideFiles = Object.keys(zip.files)
      .filter((f) => f.startsWith("ppt/slides/slide") && f.endsWith(".xml"))
      .sort();

    for (const slideFile of slideFiles) {
      const xml = await zip.files[slideFile].async("string");
      // Extract text between XML tags
      const matches = xml.match(/>([^<]+)</g);
      if (matches) {
        for (const m of matches) {
          const text = m.slice(1, -1).trim();
          if (text) texts.push(text);
        }
      }
    }
    return texts.join("\n");
  } catch {
    return null;
  }
}
