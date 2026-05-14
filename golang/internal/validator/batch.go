package validator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// BatchOptions tunes ValidateBatch.
type BatchOptions struct {
	// Concurrency overrides Validator.concurrency for this batch.
	// 0 → use the validator's default.
	Concurrency int
	// ConsensusRuns is the number of independent runs per control.
	// 0 or 1 → single run (no consensus). N>1 → run N times and majority-vote.
	ConsensusRuns int
	// Progress is invoked once per per-control completion (and per
	// consensus run, if ConsensusRuns > 1). Optional; nil disables.
	Progress func(ProgressEvent)
}

// ProgressEvent is emitted to BatchOptions.Progress as controls complete.
type ProgressEvent struct {
	Run       int // 1-indexed run number; always 1 when not in consensus mode
	TotalRuns int // ConsensusRuns or 1
	ControlID string
	Done      int // 1-indexed completed count for this run
	Total     int // controls in the run
	Result    Result
}

// ValidateBatch runs ValidateControl across controlIDs with bounded
// concurrency. Audit fix B6: there is no within-control batching; each
// control gets exactly one Converse call.
//
// Returns a map keyed by controlID. Errors from individual controls do
// not abort the batch — they surface as Result{Status: StatusErrored}.
// The function only returns an error if ctx is cancelled before any work
// can run.
func (v *Validator) ValidateBatch(ctx context.Context, controlIDs []string, partnerFolder string, opts BatchOptions) (map[string]Result, error) {
	if opts.ConsensusRuns > 1 {
		return v.validateWithConsensus(ctx, controlIDs, partnerFolder, opts)
	}

	results, err := v.runOnce(ctx, controlIDs, partnerFolder, opts, 1, 1)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (v *Validator) runOnce(ctx context.Context, controlIDs []string, partnerFolder string,
	opts BatchOptions, runIdx, totalRuns int,
) (map[string]Result, error) {
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = v.concurrency
	}

	sem := semaphore.NewWeighted(int64(concurrency))
	g, gctx := errgroup.WithContext(ctx)

	results := make(map[string]Result, len(controlIDs))
	var resultsMu sync.Mutex

	var done int32
	total := len(controlIDs)

	for _, id := range controlIDs {
		controlID := id
		if err := sem.Acquire(gctx, 1); err != nil {
			return nil, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			r := v.ValidateControl(gctx, controlID, partnerFolder)

			resultsMu.Lock()
			results[controlID] = r
			done++
			d := done
			resultsMu.Unlock()

			if opts.Progress != nil {
				opts.Progress(ProgressEvent{
					Run:       runIdx,
					TotalRuns: totalRuns,
					ControlID: controlID,
					Done:      int(d),
					Total:     total,
					Result:    r,
				})
			}
			return nil // never returned; per-control errors live in Result.Err
		})
	}

	if err := g.Wait(); err != nil {
		return results, err
	}
	return results, nil
}

// validateWithConsensus runs N rounds of validation and reduces them by
// majority vote. Audit B7 ported faithfully — see allPassedTwice for the
// asymmetry note.
func (v *Validator) validateWithConsensus(ctx context.Context, controlIDs []string, partnerFolder string, opts BatchOptions) (map[string]Result, error) {
	runs := opts.ConsensusRuns
	if runs <= 1 {
		return nil, fmt.Errorf("validator: validateWithConsensus called with ConsensusRuns=%d", runs)
	}

	allRuns := make([]map[string]Result, 0, runs)
	for i := 1; i <= runs; i++ {
		v.logger.Info("consensus run starting", slog.Int("run", i), slog.Int("total", runs))

		results, err := v.runOnce(ctx, controlIDs, partnerFolder, opts, i, runs)
		if err != nil {
			return nil, err
		}
		allRuns = append(allRuns, results)

		// preserved-from-python: when runs == 3 AND every control passed
		// in runs 1 and 2 (errored controls do NOT satisfy "passed"),
		// skip run 3. The asymmetry — there is no equivalent early-stop
		// for "all-fail-twice" — is intentional and was carried over
		// from the Python implementation. Documented in PLAN.md (B7).
		if i == 2 && runs == 3 && allPassedTwice(controlIDs, allRuns) {
			v.logger.Info("consensus early-stop: all controls passed runs 1+2")
			break
		}
	}

	return reduceConsensus(controlIDs, allRuns), nil
}

func allPassedTwice(controlIDs []string, allRuns []map[string]Result) bool {
	if len(allRuns) < 2 {
		return false
	}
	for _, id := range controlIDs {
		for _, run := range allRuns[:2] {
			r, ok := run[id]
			if !ok || r.Status != StatusPassed {
				return false
			}
		}
	}
	return true
}

// reduceConsensus applies a majority vote across runs. Tie-broken toward
// the most common verdict; ties go to FAIL (consistent with Python's
// `consensus_pass = yes_count > no_count`). Errored runs count as failures
// for vote purposes but the most recent erroring Result is preserved when
// the verdict reduces to ERRORED.
func reduceConsensus(controlIDs []string, allRuns []map[string]Result) map[string]Result {
	out := make(map[string]Result, len(controlIDs))
	for _, id := range controlIDs {
		passed := 0
		failed := 0
		var firstPass, firstFail Result
		hasPass, hasFail := false, false

		for _, run := range allRuns {
			r := run[id]
			switch r.Status {
			case StatusPassed:
				passed++
				if !hasPass {
					firstPass = r
					hasPass = true
				}
			default:
				// FAILED, WAIVED, ERRORED all count as "not pass".
				failed++
				if !hasFail {
					firstFail = r
					hasFail = true
				}
			}
		}

		if passed > failed {
			rep := firstPass
			rep.Reasoning = fmt.Sprintf("Consensus %d/%d. %s", passed, len(allRuns), rep.Reasoning)
			out[id] = rep
		} else {
			rep := firstFail
			rep.Reasoning = fmt.Sprintf("Consensus %d/%d not passing. %s", failed, len(allRuns), rep.Reasoning)
			out[id] = rep
		}
	}
	return out
}

// SortControlIDs returns ids sorted lexicographically. Convenience for
// callers that want stable iteration over results.
func SortControlIDs(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}
