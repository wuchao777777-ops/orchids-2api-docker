package orchids

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCreateProjectUsesClerkCookiesAndUpdatesState(t *testing.T) {
	t.Parallel()

	acc := &store.Account{}
	client := &Client{
		config: &config.Config{
			ClientCookie:  "client_cookie",
			SessionCookie: "session_cookie",
			ClientUat:     "1739251200",
			SessionID:     "sess_project",
		},
		account: acc,
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost {
					t.Fatalf("method=%s want POST", req.Method)
				}
				if got := req.URL.String(); got != orchidsProjectCreateURL {
					t.Fatalf("url=%q want %q", got, orchidsProjectCreateURL)
				}
				if got := req.Header.Get("Cookie"); !strings.Contains(got, "__client=client_cookie") || !strings.Contains(got, "__session=session_cookie") || !strings.Contains(got, "__client_uat=1739251200") || !strings.Contains(got, "clerk_active_context=sess_project:") {
					t.Fatalf("cookie=%q want __client/__session/__client_uat/clerk_active_context", got)
				}
				if got := req.Header.Get("Origin"); got != orchidsWSOrigin {
					t.Fatalf("origin=%q want %q", got, orchidsWSOrigin)
				}
				if got := req.Header.Get("X-Requested-With"); got != "XMLHttpRequest" {
					t.Fatalf("X-Requested-With=%q want XMLHttpRequest", got)
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"project":{"id":"proj_created"}}`)),
					Header:     make(http.Header),
				}, nil
			}),
		},
	}

	projectID, err := client.createProject(context.Background())
	if err != nil {
		t.Fatalf("createProject() error = %v", err)
	}
	if projectID != "proj_created" {
		t.Fatalf("projectID=%q want proj_created", projectID)
	}
	if client.config.ProjectID != "proj_created" {
		t.Fatalf("config.ProjectID=%q want proj_created", client.config.ProjectID)
	}
	if acc.ProjectID != "proj_created" {
		t.Fatalf("account.ProjectID=%q want proj_created", acc.ProjectID)
	}
}

func TestCreateProjectRequiresClerkCookieMaterial(t *testing.T) {
	t.Parallel()

	client := &Client{
		config:     &config.Config{ClientCookie: "client_only"},
		httpClient: &http.Client{},
	}

	_, err := client.createProject(context.Background())
	if err == nil {
		t.Fatal("expected createProject() to fail without session cookie or client_uat")
	}
	if err != errOrchidsProjectBootstrapUnavailable {
		t.Fatalf("err=%v want %v", err, errOrchidsProjectBootstrapUnavailable)
	}
}

func TestCreateProjectBootstrapsClientCookieFromSession(t *testing.T) {
	t.Parallel()

	acc := &store.Account{SessionID: "sess_bootstrap"}
	callCount := 0
	client := &Client{
		config: &config.Config{
			SessionID:      "sess_bootstrap",
			SessionCookie:  "session_cookie",
			RequestTimeout: 5,
		},
		account: acc,
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				callCount++
				switch callCount {
				case 1:
					if req.URL.Path != "/v1/client" {
						t.Fatalf("bootstrap path=%q want /v1/client", req.URL.Path)
					}
					resp := &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(`{"response":{"sessions":[]}}`)),
						Header:     make(http.Header),
					}
					resp.Header.Add("Set-Cookie", "__client=bootstrapped-client; Path=/; HttpOnly")
					resp.Header.Add("Set-Cookie", "__client_uat=1773712060; Path=/")
					return resp, nil
				case 2:
					if req.URL.String() != orchidsProjectCreateURL {
						t.Fatalf("url=%q want %q", req.URL.String(), orchidsProjectCreateURL)
					}
					if got := req.Header.Get("Cookie"); !strings.Contains(got, "__client=bootstrapped-client") || !strings.Contains(got, "__session=session_cookie") || !strings.Contains(got, "__client_uat=1773712060") || !strings.Contains(got, "clerk_active_context=sess_bootstrap:") {
						t.Fatalf("cookie=%q want bootstrapped cookies", got)
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(`{"project":{"id":"proj_bootstrap"}}`)),
						Header:     make(http.Header),
					}, nil
				default:
					t.Fatalf("unexpected extra request #%d to %s", callCount, req.URL.String())
					return nil, nil
				}
			}),
		},
	}

	projectID, err := client.createProject(context.Background())
	if err != nil {
		t.Fatalf("createProject() error = %v", err)
	}
	if projectID != "proj_bootstrap" {
		t.Fatalf("projectID=%q want proj_bootstrap", projectID)
	}
	if client.config.ClientCookie != "bootstrapped-client" {
		t.Fatalf("ClientCookie=%q want bootstrapped-client", client.config.ClientCookie)
	}
}

func TestDecodeOrchidsProjectCreateResponseExtractsProjectID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "nested project", body: `{"project":{"id":"proj_1"}}`, want: "proj_1"},
		{name: "data projectId", body: `{"data":{"projectId":"proj_2"}}`, want: "proj_2"},
		{name: "top level id", body: `{"id":"proj_3"}`, want: "proj_3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeOrchidsProjectCreateResponse([]byte(tt.body))
			if err != nil {
				t.Fatalf("decodeOrchidsProjectCreateResponse() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("projectID=%q want %q", got, tt.want)
			}
		})
	}
}
