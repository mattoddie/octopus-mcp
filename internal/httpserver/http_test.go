package httpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattoddie/octopus-mcp/internal/config"
	"github.com/mattoddie/octopus-mcp/internal/octopus"
	"github.com/mattoddie/octopus-mcp/internal/tools"
)

func newServer(t *testing.T, cfg config.HTTP) *httptest.Server {
	t.Helper()
	client := octopus.New(config.Octopus{APIKey: "unused", APIURL: "http://invalid", GraphQLURL: "http://invalid/", BackendGraphQLURL: "http://invalid/", Timeout: 1e9})
	srv := httptest.NewServer(Handler(cfg, tools.NewServer(client, config.AllTools(), nil)))
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, url, token, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

const listTools = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

func TestBearerAuthAndToolsList(t *testing.T) {
	srv := newServer(t, config.HTTP{AuthToken: "s3cret"})
	if res := post(t, srv.URL+"/mcp", "", listTools); res.StatusCode != 401 || res.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("no token: %d", res.StatusCode)
	}
	if res := post(t, srv.URL+"/mcp", "wrong", listTools); res.StatusCode != 401 {
		t.Errorf("wrong token: %d", res.StatusCode)
	}
	res := post(t, srv.URL+"/mcp", "s3cret", listTools)
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	var out struct {
		Result struct {
			Tools []struct{ Name string } `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Result.Tools) < 20 {
		t.Errorf("tools: %d", len(out.Result.Tools))
	}
}

func TestHealthAndNotFound(t *testing.T) {
	srv := newServer(t, config.HTTP{AuthToken: "x"})
	res, err := http.Get(srv.URL + "/health")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("health: %v %v", res, err)
	}
	res.Body.Close()
	res, _ = http.Get(srv.URL + "/other")
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("other: %d", res.StatusCode)
	}
}

func TestAllowedHosts(t *testing.T) {
	srv := newServer(t, config.HTTP{AllowedHosts: []string{"mcp.example.com"}})
	if res := post(t, srv.URL+"/mcp", "", listTools); res.StatusCode != 403 {
		t.Errorf("unlisted host: %d", res.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(listTools))
	req.Host = "mcp.example.com:443"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("listed host: %d", res.StatusCode)
	}
}

func send(t *testing.T, method, url, body string, headers map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func TestMethodsAndBodies(t *testing.T) {
	srv := newServer(t, config.HTTP{AuthToken: "s3cret"})
	auth := map[string]string{"Authorization": "Bearer s3cret"}
	for _, m := range []string{"GET", "DELETE", "PUT"} {
		res, body := send(t, m, srv.URL+"/mcp", "", auth)
		if res.StatusCode != 405 || res.Header.Get("Allow") != "POST" || !strings.Contains(body, "Method not allowed") {
			t.Errorf("%s: %d %s", m, res.StatusCode, body)
		}
	}
	// Unauthenticated requests are refused before the method is looked at.
	if res, _ := send(t, "GET", srv.URL+"/mcp", "", nil); res.StatusCode != 401 {
		t.Errorf("GET without a token: %d", res.StatusCode)
	}
	for _, b := range []string{"{not json", ""} {
		if res, body := send(t, "POST", srv.URL+"/mcp", b, auth); res.StatusCode != 400 || !strings.Contains(body, "Invalid JSON body") {
			t.Errorf("%q: %d %s", b, res.StatusCode, body)
		}
	}
	big := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"pad":"` + strings.Repeat("a", MaxBodyBytes) + `"}}}`
	if res, body := send(t, "POST", srv.URL+"/mcp", big, auth); res.StatusCode != 413 || !strings.Contains(body, "Request body too large") {
		t.Errorf("large body: %d %s", res.StatusCode, body)
	}
	near := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"pad":"` + strings.Repeat("a", 3<<20) + `"}}}`
	if res, _ := send(t, "POST", srv.URL+"/mcp", near, auth); res.StatusCode != 200 {
		t.Errorf("3 MiB body: %d", res.StatusCode)
	}
}

func TestAllowedHostsAndOrigins(t *testing.T) {
	srv := newServer(t, config.HTTP{AuthToken: "s3cret", AllowedHosts: []string{"127.0.0.1", "mcp.example.com", "app.example.com:3000", "[::1]", "tls.example.com:443"}})
	with := func(kv ...string) map[string]string {
		h := map[string]string{"Authorization": "Bearer s3cret"}
		for i := 0; i+1 < len(kv); i += 2 {
			h[kv[i]] = kv[i+1]
		}
		return h
	}
	for host, want := range map[string]int{
		"":                     200, // the test server's own 127.0.0.1:<port>
		"mcp.example.com:443":  200, // an entry without a port matches any port
		"MCP.example.com":      200,
		"evil.example:80":      403,
		"app.example.com:3000": 200,
		"app.example.com:3001": 403,
		"app.example.com":      403,
		"[::1]:8080":           200,
		"mcp.example.com.evil": 403,
	} {
		h := with()
		if host != "" {
			h["Host"] = host
		}
		if res, body := send(t, "POST", srv.URL+"/mcp", listTools, h); res.StatusCode != want {
			t.Errorf("Host %q: %d, want %d (%s)", host, res.StatusCode, want, body)
		}
	}
	for origin, want := range map[string]int{
		srv.URL:                        403, // 127.0.0.1 on a non-default port isn't listed
		"http://127.0.0.1":             200,
		"http://127.0.0.1:80":          200,
		"https://127.0.0.1":            200,
		"https://127.0.0.1:443":        200,
		"http://127.0.0.1:443":         403,
		"http://localhost":             403,
		"https://mcp.example.com":      200,
		"https://mcp.example.com:8443": 403,
		"http://app.example.com:3000":  200,
		"https://app.example.com:3000": 200,
		"http://app.example.com":       403,
		"https://tls.example.com":      200,
		"http://tls.example.com":       403,
		"http://[::1]":                 200,
		"http://[::1]:3000":            403,
		"":                             403,
		"null":                         403,
		"file://":                      403,
		"https://user@mcp.example.com": 403,
		"mcp.example.com":              403,
	} {
		if res, body := send(t, "POST", srv.URL+"/mcp", listTools, with("Origin", origin)); res.StatusCode != want {
			t.Errorf("Origin %q: %d, want %d (%s)", origin, res.StatusCode, want, body)
		}
	}
	// Without an allow-list, Origin isn't checked here (the SDK's localhost protection applies instead).
	open := newServer(t, config.HTTP{AuthToken: "s3cret"})
	if res, _ := send(t, "POST", open.URL+"/mcp", listTools, with("Origin", "http://127.0.0.1")); res.StatusCode != 200 {
		t.Errorf("no allow-list: %d", res.StatusCode)
	}
}
