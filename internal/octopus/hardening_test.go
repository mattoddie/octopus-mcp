package octopus_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/config"
	"github.com/mattoddie/octopus-mcp/internal/octopus"
	"github.com/mattoddie/octopus-mcp/internal/octopus/octopustest"
)

func TestRedirectsAreNotFollowed(t *testing.T) {
	var reached atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		w.Write([]byte(`{}`))
	}))
	defer other.Close()
	same := httptest.NewServer(nil)
	defer same.Close()
	same.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/accounts/A-1234ABCD/" {
			// Even a same-host redirect isn't followed (it could downgrade to http).
			http.Redirect(w, r, same.URL+"/v1/elsewhere/", http.StatusFound)
			return
		}
		if r.URL.Path == "/v1/products/" {
			http.Redirect(w, r, other.URL+"/v1/products/", http.StatusMovedPermanently)
			return
		}
		reached.Add(1)
		w.Write([]byte(`{}`))
	})
	c := octopus.New(config.Octopus{APIKey: octopustest.APIKey, APIURL: same.URL, GraphQLURL: same.URL + "/v1/graphql/", Timeout: 5 * time.Second})
	for _, p := range []string{"/accounts/A-1234ABCD/", "/products/"} {
		err := c.Get(context.Background(), p, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "redirect") {
			t.Errorf("%s: %v", p, err)
		}
	}
	if reached.Load() != 0 {
		t.Errorf("a redirect was followed %d times", reached.Load())
	}
}

func TestOversizedResponsesAreRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":"`))
		chunk := []byte(strings.Repeat("a", 1<<20))
		for i := 0; i <= octopus.MaxResponseBytes>>20; i++ {
			w.Write(chunk)
		}
		w.Write([]byte(`"}`))
	}))
	defer srv.Close()
	c := octopus.New(config.Octopus{APIKey: octopustest.APIKey, APIURL: srv.URL, Timeout: 30 * time.Second})
	var out any
	if err := c.GetPublic(context.Background(), "/products/", nil, &out); err == nil || !strings.Contains(err.Error(), "larger than 64 MiB") {
		t.Errorf("oversized: %v", err)
	}
}

// gqlServer issues a token and answers every other GraphQL request with status.
func gqlServer(t *testing.T, status int, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "obtainKrakenToken") {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"obtainKrakenToken": map[string]any{"token": "token-abcdefghijk", "refreshToken": "refresh-abcdefghijk"}}})
			return
		}
		calls.Add(1)
		w.WriteHeader(status)
		w.Write([]byte(`{"detail":"busy"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMutationsAreNotRetriedOn503(t *testing.T) {
	var calls atomic.Int32
	srv := gqlServer(t, http.StatusServiceUnavailable, &calls)
	c := octopus.New(config.Octopus{APIKey: octopustest.APIKey, APIURL: srv.URL, GraphQLURL: srv.URL + "/v1/graphql/", Timeout: 5 * time.Second})
	err := c.GraphQL(context.Background(), `mutation { updateBoostCharge(input: {}) { id } }`, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") || calls.Load() != 1 {
		t.Errorf("503 on POST: %v after %d calls", err, calls.Load())
	}

	// GET is still retried on 503.
	var gets atomic.Int32
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gets.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer rest.Close()
	c = octopus.New(config.Octopus{APIURL: rest.URL, Timeout: 5 * time.Second})
	if err := c.GetPublic(context.Background(), "/products/", nil, nil); err != nil || gets.Load() != 2 {
		t.Errorf("503 on GET: %v after %d calls", err, gets.Load())
	}
}

func TestPostsAreRetriedWhenRateLimited(t *testing.T) {
	var calls atomic.Int32
	srv := gqlServer(t, http.StatusTooManyRequests, &calls)
	c := octopus.New(config.Octopus{APIKey: octopustest.APIKey, APIURL: srv.URL, GraphQLURL: srv.URL + "/v1/graphql/", Timeout: 5 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := c.GraphQL(ctx, `query { a }`, nil, nil)
	if err == nil || calls.Load() != 4 {
		t.Errorf("429 on POST: %v after %d calls", err, calls.Load())
	}
}

func TestGraphQLErrorExtensionsAreRedacted(t *testing.T) {
	m, c := newClient(t)
	var first atomic.Value
	m.OnGraphQL(`query Leak`, func(r octopustest.GraphQLRequest) (any, []any) {
		first.CompareAndSwap(nil, r.Token)
		old := first.Load().(string)
		return nil, []any{map[string]any{
			"message":    "bad " + r.Token,
			"extensions": map[string]any{"errorCode": "KT-CT-4321", "errorDescription": "current " + r.Token + " old " + old + " key " + octopustest.APIKey, "boostChargeRefusalReasons": []any{"BC_" + r.Token}},
		}}
	})
	ctx := context.Background()
	err := c.GraphQL(ctx, `query Leak { a }`, nil, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	firstToken := first.Load().(string)
	// After a refresh, the old token is still scrubbed.
	m.ExpireTokens()
	err2 := c.GraphQL(ctx, `query Leak { a }`, nil, nil)
	for _, e := range []error{err, err2} {
		msg := e.Error()
		if strings.Contains(msg, "eyJ") || strings.Contains(msg, firstToken) || strings.Contains(msg, octopustest.APIKey) || !strings.Contains(msg, "[redacted]") {
			t.Errorf("leak: %s", msg)
		}
	}
	if m.TokenRequests != 2 {
		t.Errorf("token requests: %d", m.TokenRequests)
	}
}

func TestRejectedAPIKeyBacksOff(t *testing.T) {
	m := octopustest.New()
	defer m.Server.Close()
	cfg := m.Config()
	cfg.APIKey = "not-the-right-key-0123"
	c := octopus.New(cfg)
	now := time.Now()
	c.Now = func() time.Time { return now }
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := c.GraphQL(ctx, `query { a }`, nil, nil); err == nil || !strings.Contains(err.Error(), "KT-CT-1135") {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if m.TokenRequests != 1 {
		t.Errorf("a rejected key was retried: %d token requests", m.TokenRequests)
	}
	now = now.Add(31 * time.Second)
	_ = c.GraphQL(ctx, `query { a }`, nil, nil)
	if m.TokenRequests != 2 {
		t.Errorf("no retry after the backoff: %d token requests", m.TokenRequests)
	}
}
