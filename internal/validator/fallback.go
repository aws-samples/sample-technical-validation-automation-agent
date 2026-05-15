package validator

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// FallbackBatchSize is the number of documents sent per batch during
// fallback validation. Mirrors the Python version's batch_size=5 default
// for small files.
const FallbackBatchSize = 4

// ShouldFallback returns true if a failed result's reasoning suggests
// the evidence map routed the wrong documents to this control. These
// controls should be retried with all supporting docs in batches.
//
// IMPORTANT: This should only trigger for evidence-routing issues, NOT
// for cases where the partner genuinely didn't provide sufficient detail
// in their written response. Words like "missing", "lacks", "doesn't
// mention" appear in legitimate failures and should NOT trigger fallback.
func ShouldFallback(r Result) bool {
	if r.Status != StatusFailed {
		return false
	}
	lower := strings.ToLower(r.Reasoning)

	// Only trigger on clear evidence-routing indicators — cases where
	// the model explicitly says the documents don't match or are about
	// a different topic than the partner's response.
	indicators := []string{
		"different project",
		"different customer",
		"different engagement",
		"unrelated document",
		"unrelated file",
		"wrong document",
		"document mismatch",
		"evidence mismatch",
		"documents provided do not",
		"documents do not match",
		"supporting documents are about",
		"supporting docs are about",
	}
	for _, ind := range indicators {
		if strings.Contains(lower, ind) {
			return true
		}
	}
	return false
}

// ValidateControlWithFallback retries a single control using ALL
// supporting docs in batches (like the Python version). It sends files
// in groups of FallbackBatchSize, trying each batch until one produces
// a PASS or all batches are exhausted.
func (v *Validator) ValidateControlWithFallback(ctx context.Context, controlID, partnerFolder string) Result {
	partnerResp, err := v.PartnerResponse(partnerFolder, controlID)
	if err != nil {
		return errResult(controlID, err, "load partner_responses.csv")
	}

	promptContext, ok := v.contextByID[controlID]
	if !ok {
		return errResult(controlID, nil, "control %s not found in CONTEXT.csv", controlID)
	}

	supportingDir := filepath.Join(partnerFolder, "supporting_docs")
	allFiles, err := listEvidenceFiles(supportingDir)
	if err != nil {
		return errResult(controlID, err, "enumerate supporting_docs for fallback")
	}

	if len(allFiles) == 0 {
		// No files to try — return original failure
		return Result{
			ControlID: controlID,
			Status:    StatusFailed,
			Reasoning: "No supporting documents available for fallback validation",
		}
	}

	v.logger.Info("fallback: retrying with batched docs",
		slog.String("control_id", controlID),
		slog.Int("total_files", len(allFiles)),
		slog.Int("batch_size", FallbackBatchSize))

	// Try batches of files until one produces a PASS
	var lastResult Result
	for i := 0; i < len(allFiles); i += FallbackBatchSize {
		end := i + FallbackBatchSize
		if end > len(allFiles) {
			end = len(allFiles)
		}
		batch := allFiles[i:end]

		contentBlocks, err := v.processFiles(batch)
		if err != nil {
			v.logger.Warn("fallback: batch processing failed, trying next batch",
				slog.String("control_id", controlID),
				slog.Int("batch_start", i),
				slog.String("err", err.Error()))
			continue
		}

		callCtx, cancel := context.WithTimeout(ctx, v.perCallTO)
		out, err := v.bedrock.Converse(callCtx, &bedrockruntime.ConverseInput{
			ModelId:  aws.String(v.modelID),
			System:   []bedrocktypes.SystemContentBlock{&bedrocktypes.SystemContentBlockMemberText{Value: string(v.systemPrompt)}},
			Messages: []bedrocktypes.Message{userMessage(partnerResp, controlID, promptContext, contentBlocks)},
			InferenceConfig: &bedrocktypes.InferenceConfiguration{
				MaxTokens:   aws.Int32(2000),
				Temperature: aws.Float32(0.0),
			},
		})
		cancel()

		if err != nil {
			v.logger.Warn("fallback: bedrock call failed for batch, trying next",
				slog.String("control_id", controlID),
				slog.Int("batch_start", i),
				slog.String("err", err.Error()))
			lastResult = errResult(controlID, err, "fallback bedrock converse batch %d", i/FallbackBatchSize+1)
			continue
		}

		raw := extractAssistantText(out)
		result := parseResult(controlID, raw)
		result.Reasoning = fmt.Sprintf("[fallback batch %d/%d] %s",
			i/FallbackBatchSize+1, (len(allFiles)+FallbackBatchSize-1)/FallbackBatchSize, result.Reasoning)

		if result.Status == StatusPassed {
			v.logger.Info("fallback: PASSED on batch",
				slog.String("control_id", controlID),
				slog.Int("batch_num", i/FallbackBatchSize+1))
			return result
		}
		lastResult = result
	}

	// All batches exhausted without a PASS
	v.logger.Info("fallback: all batches exhausted without PASS",
		slog.String("control_id", controlID))
	return lastResult
}

// ValidateBatchWithFallback runs the standard evidence-mapped validation
// first, then retries any failed controls that appear to have evidence
// mismatch issues using the batched fallback approach.
func (v *Validator) ValidateBatchWithFallback(ctx context.Context, controlIDs []string, partnerFolder string, opts BatchOptions) (map[string]Result, error) {
	// Phase 1: Standard evidence-mapped validation
	results, err := v.ValidateBatch(ctx, controlIDs, partnerFolder, opts)
	if err != nil {
		return nil, err
	}

	// Phase 2: Identify controls that need fallback
	var fallbackIDs []string
	for _, id := range controlIDs {
		r, ok := results[id]
		if !ok {
			continue
		}
		if ShouldFallback(r) {
			fallbackIDs = append(fallbackIDs, id)
		}
	}

	if len(fallbackIDs) == 0 {
		return results, nil
	}

	v.logger.Info("fallback phase: retrying failed controls with batched docs",
		slog.Int("fallback_count", len(fallbackIDs)),
		slog.Int("total_controls", len(controlIDs)))

	if opts.Progress != nil {
		opts.Progress(ProgressEvent{
			ControlID: fmt.Sprintf("FALLBACK_START:%d", len(fallbackIDs)),
			Done:      0,
			Total:     len(fallbackIDs),
		})
	}

	// Phase 2: Retry with fallback (parallel with limited concurrency)
	sem := make(chan struct{}, 3) // limit to 3 concurrent fallback retries
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i, id := range fallbackIDs {
		wg.Add(1)
		go func(idx int, controlID string) {
			defer wg.Done()
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release

			result := v.ValidateControlWithFallback(ctx, controlID, partnerFolder)

			mu.Lock()
			results[controlID] = result
			mu.Unlock()

			if opts.Progress != nil {
				opts.Progress(ProgressEvent{
					ControlID: controlID,
					Done:      idx + 1,
					Total:     len(fallbackIDs),
					Result:    result,
				})
			}
		}(i, id)
	}
	wg.Wait()

	return results, nil
}
