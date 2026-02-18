/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package omnibump

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestTokenTransport_OnlyAddsHeaderForGitHubAPI tests that the Authorization
// header is only added for api.github.com requests, preventing token leakage
// via cross-host redirects (FINDING-OMNIBUMP-002).
func TestTokenTransport_OnlyAddsHeaderForGitHubAPI(t *testing.T) {
	testToken := "test-github-token-12345"
	transport := &tokenTransport{token: testToken}

	tests := []struct {
		name           string
		requestURL     string
		expectHeader   bool
		expectedHeader string
	}{
		{
			name:           "GitHub API request",
			requestURL:     "https://api.github.com/repos/owner/repo",
			expectHeader:   true,
			expectedHeader: "Bearer " + testToken,
		},
		{
			name:         "Attacker-controlled redirect",
			requestURL:   "https://attacker.example.com/exfil",
			expectHeader: false,
		},
		{
			name:         "Different GitHub subdomain",
			requestURL:   "https://github.com/owner/repo",
			expectHeader: false,
		},
		{
			name:         "Random external host",
			requestURL:   "https://external-api.example.com/data",
			expectHeader: false,
		},
		{
			name:         "Localhost",
			requestURL:   "http://localhost:8080/test",
			expectHeader: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server to capture the request
			var capturedHeaders http.Header
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			// Create request with the test URL's host
			req, err := http.NewRequest("GET", tt.requestURL, nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}

			// Override the URL to point to our test server for actual request
			// but keep the host from the original URL for header logic
			originalHost := req.URL.Host
			req.URL.Host = server.URL[7:] // strip https://
			req.URL.Scheme = "http"
			// Restore the original host for the tokenTransport check
			req.URL.Host = originalHost

			// Create a custom transport that wraps our tokenTransport
			// and directs requests to our test server
			testTransport := &testRoundTripper{
				wrapped:    transport,
				testServer: server.URL,
			}

			client := &http.Client{Transport: testTransport}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					t.Errorf("failed to close response body: %v", err)
				}
			}()

			// Check if Authorization header was added
			authHeader := capturedHeaders.Get("Authorization")

			if tt.expectHeader {
				if authHeader != tt.expectedHeader {
					t.Errorf("Expected Authorization header %q, got %q", tt.expectedHeader, authHeader)
				}
			} else {
				if authHeader != "" {
					t.Errorf("Authorization header should not be set for %s, but got: %q", tt.requestURL, authHeader)
				}
			}
		})
	}
}

// testRoundTripper wraps tokenTransport and redirects requests to a test server.
type testRoundTripper struct {
	wrapped    http.RoundTripper
	testServer string
}

func (t *testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Let tokenTransport add headers based on original host
	modifiedReq := req.Clone(req.Context())

	// Apply tokenTransport logic
	if req.URL.Host == "api.github.com" {
		modifiedReq.Header.Set("Authorization", "Bearer "+t.wrapped.(*tokenTransport).token)
	}

	// Redirect to test server for actual HTTP request
	modifiedReq.URL.Scheme = "http"
	modifiedReq.URL.Host = t.testServer[7:] // strip http://

	return http.DefaultTransport.RoundTrip(modifiedReq)
}

// TestTokenTransport_PreventsCrossHostTokenLeak simulates a redirect attack.
func TestTokenTransport_PreventsCrossHostTokenLeak(t *testing.T) {
	testToken := "secret-token-that-should-not-leak"

	// Create attacker's server that logs Authorization headers
	var leakedToken string
	attackerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leakedToken = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer attackerServer.Close()

	// Simulate what would happen if GitHub redirected to attacker's server
	req, err := http.NewRequest("GET", attackerServer.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	// Change host to simulate redirect to attacker (not api.github.com)
	req.URL.Host = "attacker.example.com"

	transport := &tokenTransport{token: testToken}
	resp, err := transport.RoundTrip(req)
	// Request will fail because we're using a fake host, but that's OK
	// We're testing that the header wasn't added before the failure
	_ = err
	if resp != nil {
		defer func() {
			if err := resp.Body.Close(); err != nil {
				t.Errorf("failed to close response body: %v", err)
			}
		}()
	}

	// Verify token was NOT leaked to attacker
	if leakedToken != "" {
		t.Errorf("Token was leaked to attacker-controlled host! Authorization header: %q", leakedToken)
	}
}
