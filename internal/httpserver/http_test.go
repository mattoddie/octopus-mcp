package httpserver

import (
	"encoding/json"
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
