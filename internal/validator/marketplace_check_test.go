package validator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// interceptingTransport rewrites every outgoing request to hit srv,
// preserving the original URL.Path so the inner handler can match on it.
// This lets the marketplace regex see real `aws.amazon.com/...` strings
// while the HTTP traffic stays in-process.
type interceptingTransport struct {
	srv *httptest.Server
}

func (it *interceptingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, err := url.Parse(it.srv.URL)
	if err != nil {
		return nil, err
	}
	clone := req.Clone(req.Context())
	clone.URL.Scheme = target.Scheme
	clone.URL.Host = target.Host
	clone.Host = target.Host
	return it.srv.Client().Transport.RoundTrip(clone)
}

func newTestClient(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &http.Client{
		Transport: &interceptingTransport{srv: srv},
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if isAWSAmazonHost(req.URL) {
				return nil
			}
			return errRedirectRefused
		},
	}
}

// ------------------------------------------------------------------
// B3: empty partner response → WAIVED
// ------------------------------------------------------------------

func TestMarketplaceCheck_EmptyResponseWaives_B3(t *testing.T) {
	client := http.DefaultClient
	r := runMarketplaceCheck(context.Background(), client, "")
	if r.Status != StatusWaived {
		t.Errorf("status = %v, want StatusWaived", r.Status)
	}
	if !strings.Contains(r.Reasoning, "manual review") {
		t.Errorf("reasoning %q should mention manual review", r.Reasoning)
	}
}

func TestMarketplaceCheck_WhitespaceOnlyWaives(t *testing.T) {
	r := runMarketplaceCheck(context.Background(), http.DefaultClient, "   \n\t")
	if r.Status != StatusWaived {
		t.Errorf("status = %v, want StatusWaived", r.Status)
	}
}

// ------------------------------------------------------------------
// B23: anchored N/A detection
// ------------------------------------------------------------------

func TestMarketplaceCheck_NAResponsesPass_B23(t *testing.T) {
	for _, in := range []string{"N/A", "n/a", "Not Applicable", "not applicable", "Not Available"} {
		r := runMarketplaceCheck(context.Background(), http.DefaultClient, in)
		if r.Status != StatusPassed {
			t.Errorf("input %q: status = %v, want StatusPassed", in, r.Status)
		}
	}
}

func TestMarketplaceCheck_NAFalsePositiveBlocked_B23(t *testing.T) {
	// Phrase that contains an N/A substring but is not the entire response.
	// In Python this falsely passed as N/A. The Go port must NOT.
	r := runMarketplaceCheck(context.Background(), http.DefaultClient,
		"We have an N/A backup plan for any Marketplace integration issues that arise during deployment")
	if r.Status == StatusPassed {
		t.Error("phrase containing N/A as substring must not auto-pass (B23)")
	}
}

// ------------------------------------------------------------------
// "no URL provided" branches
// ------------------------------------------------------------------

func TestMarketplaceCheck_NoURLNoMentionPasses(t *testing.T) {
	r := runMarketplaceCheck(context.Background(), http.DefaultClient,
		"Our solution is sold via direct sales contracts only.")
	if r.Status != StatusPassed {
		t.Errorf("status = %v, want StatusPassed (optional requirement)", r.Status)
	}
}

func TestMarketplaceCheck_MentionsMarketplaceWithoutURLFails(t *testing.T) {
	r := runMarketplaceCheck(context.Background(), http.DefaultClient,
		"We are listed on AWS Marketplace, please see attached.")
	if r.Status != StatusFailed {
		t.Errorf("status = %v, want StatusFailed when Marketplace is mentioned without URL", r.Status)
	}
}

// ------------------------------------------------------------------
// B14: validate every URL — first 4xx fails the control
// ------------------------------------------------------------------

func TestMarketplaceCheck_AllURLsValidatedPasses_B14(t *testing.T) {
	hits := 0
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	})
	body := "See https://aws.amazon.com/marketplace/pp/abc123 and also https://aws.amazon.com/marketplace/pp/xyz999"

	r := runMarketplaceCheck(context.Background(), client, body)
	if r.Status != StatusPassed {
		t.Errorf("status = %v (%s), want StatusPassed", r.Status, r.Reasoning)
	}
	if hits != 2 {
		t.Errorf("HEAD requests = %d, want 2 (B14: every URL checked)", hits)
	}
}

func TestMarketplaceCheck_FirstBadURLFails_B14(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "abc123") {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	body := "Listings: https://aws.amazon.com/marketplace/pp/abc123 https://aws.amazon.com/marketplace/pp/missing"

	r := runMarketplaceCheck(context.Background(), client, body)
	if r.Status != StatusFailed {
		t.Errorf("status = %v, want StatusFailed when any URL returns 4xx", r.Status)
	}
	if !strings.Contains(r.Reasoning, "404") {
		t.Errorf("reasoning should mention status code: %q", r.Reasoning)
	}
}

// ------------------------------------------------------------------
// B22 deferred: narrow regex — URLs outside /pp/ are not picked up.
// ------------------------------------------------------------------

func TestMarketplaceCheck_NarrowRegex_B22Deferred(t *testing.T) {
	// A seller-profile URL that the narrow regex intentionally misses.
	// Matching the Python behaviour. Since the partner *did* mention
	// "marketplace" but provided no /pp/ URL, fail.
	r := runMarketplaceCheck(context.Background(), http.DefaultClient,
		"See our seller profile: https://aws.amazon.com/marketplace/seller-profile?id=999")
	if r.Status != StatusFailed {
		t.Errorf("status = %v, want StatusFailed (B22 narrow regex)", r.Status)
	}
}

// ------------------------------------------------------------------
// B10: redirect host allowlist
// ------------------------------------------------------------------

func TestIsAWSAmazonHost(t *testing.T) {
	cases := map[string]bool{
		"https://aws.amazon.com":              true,
		"https://aws.amazon.com/foo":          true,
		"https://marketplace.aws.amazon.com":  true, // subdomain allowed
		"https://us-east-1.aws.amazon.com":    true,
		"https://amazon.com":                  false,
		"https://aws.amazon.com.evil.example": false,
		"https://example.com":                 false,
	}
	for raw, want := range cases {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := isAWSAmazonHost(u); got != want {
			t.Errorf("isAWSAmazonHost(%q) = %v, want %v", raw, got, want)
		}
	}
}

// Sanity: the production checker constructor wires CheckRedirect — make
// sure it doesn't panic and returns a non-nil client.
func TestNewHTTPMarketplaceChecker_HasSafeRedirectPolicy(t *testing.T) {
	c := newHTTPMarketplaceChecker()
	if c == nil || c.client == nil {
		t.Fatal("constructor returned nil")
	}
	if c.client.CheckRedirect == nil {
		t.Fatal("client must set CheckRedirect for B10 host allowlist")
	}
	// Manually probe the redirect policy for a hostile host.
	req := &http.Request{URL: mustURL(t, "https://evil.example.com/foo")}
	if err := c.client.CheckRedirect(req, nil); err == nil {
		t.Error("CheckRedirect should refuse non-aws.amazon.com hosts")
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
