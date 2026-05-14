package validator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// MarketplaceChecker is the surface used by the validator for the AWS
// Marketplace listing check. The Check method is control-agnostic:
// applying it to a new control ID is a one-line registry edit (see
// specialChecks in validator.go), no logic changes required.
type MarketplaceChecker interface {
	Check(ctx context.Context, partnerResponse string) Result
}

// marketplaceURLRE matches the narrow pattern used by the Python port.
//
// Audit B22 deferred: subdomains (marketplace.aws.amazon.com), seller-
// profile URLs, and query-string variants are not matched. KNOWN-ISSUES.md
// tracks this for follow-up. Keeping the regex narrow preserves Python
// parity and avoids false positives in partner prose.
var marketplaceURLRE = regexp.MustCompile(`https?://(?:www\.)?aws\.amazon\.com/marketplace/pp/[^\s,)"']+`)

// naMarkers matches the partner-response strings that should resolve as
// "not on Marketplace" (B23 fix). Anchored to the entire trimmed input;
// "We have an N/A backup plan" no longer false-positives.
var naMarkers = map[string]struct{}{
	"n/a":            {},
	"not applicable": {},
	"not available":  {},
}

// httpMarketplaceChecker is the production MarketplaceChecker.
// Audit fixes:
//   - B3: empty partner response → WAIVED with manual-review reason.
//   - B10: redirects must land on aws.amazon.com (or a subdomain). Other
//     hosts are refused. Header response size is capped to defang
//     malicious or malformed responses.
//   - B14: every Marketplace URL is checked; a single 4xx fails the control.
//   - B23: N/A detection is anchored, not a substring match.
type httpMarketplaceChecker struct {
	client *http.Client
}

func newHTTPMarketplaceChecker() *httpMarketplaceChecker {
	transport := &http.Transport{
		MaxResponseHeaderBytes: 64 * 1024,
	}
	return &httpMarketplaceChecker{
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
			CheckRedirect: func(req *http.Request, _ []*http.Request) error {
				if isAWSAmazonHost(req.URL) {
					return nil
				}
				return fmt.Errorf("marketplace: refusing redirect to non-aws.amazon.com host: %s", req.URL.Host)
			},
		},
	}
}

func (h *httpMarketplaceChecker) Check(ctx context.Context, partnerResponse string) Result {
	return runMarketplaceCheck(ctx, h.client, partnerResponse)
}

// runMarketplaceCheck is the pure function (no client construction) the
// production checker delegates to. Tests can drive it directly with a
// stubbed *http.Client.
func runMarketplaceCheck(ctx context.Context, client *http.Client, partnerResponse string) Result {
	trimmed := strings.TrimSpace(partnerResponse)

	// B3: empty partner response → WAIVED. Manual review required.
	if trimmed == "" {
		return Result{
			Status:    StatusWaived,
			Reasoning: "no partner response provided — manual review required",
		}
	}

	// B23: anchored N/A detection. The whole trimmed response must be one
	// of the N/A markers AND under 50 chars. "We have an N/A backup plan"
	// is 25 chars but is not equal to any marker, so it falls through.
	if len(trimmed) < 50 {
		if _, ok := naMarkers[strings.ToLower(trimmed)]; ok {
			return Result{
				Status:    StatusPassed,
				Reasoning: "Partner confirms solution is not on AWS Marketplace (which is acceptable)",
			}
		}
	}

	// B22: narrow URL regex. KNOWN-ISSUES.md tracks the broader-pattern
	// follow-up.
	urls := marketplaceURLRE.FindAllString(partnerResponse, -1)
	if len(urls) == 0 {
		// No URL — fall back to the substring check Python performs.
		// Mentions "marketplace" but no URL → fail.
		if strings.Contains(strings.ToLower(partnerResponse), "marketplace") {
			return Result{
				Status:    StatusFailed,
				Reasoning: "Partner mentions AWS Marketplace but does not provide URL",
			}
		}
		return Result{
			Status:    StatusPassed,
			Reasoning: "No mention of AWS Marketplace (optional requirement)",
		}
	}

	// B14: validate every URL. First 4xx fails the control.
	var checkedURLs []string
	for _, u := range urls {
		status, err := headURL(ctx, client, u)
		switch {
		case err != nil:
			return Result{
				Status:    StatusFailed,
				Reasoning: fmt.Sprintf("AWS Marketplace URL provided but not accessible: %s (%v)", u, err),
			}
		case status >= 400 && status < 500:
			return Result{
				Status:    StatusFailed,
				Reasoning: fmt.Sprintf("AWS Marketplace URL provided but listing not found (HTTP %d): %s", status, u),
			}
		case status >= 500:
			return Result{
				Status:    StatusFailed,
				Reasoning: fmt.Sprintf("AWS Marketplace URL provided but server error (HTTP %d): %s", status, u),
			}
		}
		checkedURLs = append(checkedURLs, u)
	}
	return Result{
		Status:    StatusPassed,
		Reasoning: fmt.Sprintf("AWS Marketplace listing(s) provided and accessible: %s", strings.Join(checkedURLs, ", ")),
	}
}

// headURL issues a HEAD request and returns the response status code.
func headURL(ctx context.Context, client *http.Client, rawURL string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Thor-CLI/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}

// isAWSAmazonHost returns true when host is `aws.amazon.com` or any
// subdomain of it. Anything else is rejected by the redirect policy
// (B10).
func isAWSAmazonHost(u *url.URL) bool {
	if u == nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "aws.amazon.com" {
		return true
	}
	if strings.HasSuffix(host, ".aws.amazon.com") {
		return true
	}
	return false
}

// errRedirectRefused makes it cheap to match transport-level rejections in tests.
var errRedirectRefused = errors.New("marketplace: redirect refused by host allowlist")
