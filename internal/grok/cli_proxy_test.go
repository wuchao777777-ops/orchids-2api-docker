package grok

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"orchids-api/internal/config"
	"orchids-api/internal/testutil"
)

func TestGrokClientsUseConfiguredProxy(t *testing.T) {
	// A local forwarder is enough to assert the outgoing transport's route;
	// no real upstream credentials or network access are needed.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer proxy.Close()
	target := "http://upstream.invalid/device"
	cfg := &config.Config{ProxyURL: proxy.URL}

	cli := NewCLIClient(cfg)
	testutil.False(t, cli.httpClient == nil, "missing Grok CLI client")
	for _, tc := range []struct {
		name   string
		client *http.Client
	}{
		{name: "cli", client: cli.httpClient},
		{name: "device", client: NewDeviceAuthenticator(cfg).httpClient},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
			testutil.NoError(t, err)
			resp, err := tc.client.Do(req)
			testutil.NoError(t, err, "client did not reach configured proxy: %v")
			defer resp.Body.Close()
			testutil.Equal(t, resp.StatusCode, http.StatusTeapot)
		})
	}

	// Proxy changes must allocate a new shared browser transport.
	other := &config.Config{ProxyURL: "http://different.proxy.invalid:3128"}
	testutil.NotEqual(t, cli.httpClient, NewCLIClient(other).httpClient)
	testutil.NotEqual(t, NewDeviceAuthenticator(cfg).httpClient, NewDeviceAuthenticator(other).httpClient)
}
