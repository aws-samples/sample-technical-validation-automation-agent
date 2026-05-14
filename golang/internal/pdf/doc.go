// Package pdf splits oversized PDFs into Bedrock-compliant chunks (via
// pdfcpu) and falls back to text extraction for the pathological case
// where a single page exceeds Bedrock's document block limit.
package pdf
