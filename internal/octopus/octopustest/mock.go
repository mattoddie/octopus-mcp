// Package octopustest is a fake Octopus Energy REST API and Kraken GraphQL API for tests.
package octopustest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/config"
)

// APIKey is the only API key the mock accepts. Account is a sample account number.
const (
	APIKey  = "sk_test_0123456789abcdef"
	Account = "A-1234ABCD"
)

// Request is a recorded REST request.
type Request struct {
	Method string
	// Path is relative to /v1, e.g. /accounts/A-1234ABCD/.
	Path      string
	Query     url.Values
	BasicUser string // the HTTP Basic username, "" if none
}

// GraphQLRequest is a recorded GraphQL request.
type GraphQLRequest struct {
	Endpoint  string // "main" (Kraken) or "backend" (Octopus Energy backend)
	Query     string
	Variables map[string]any
	Token     string // the Authorization header
}

// Handler answers a matched REST request with a status and a JSON body.
type Handler func(r Request, match []string) (int, any)

// GraphQLHandler answers a matched GraphQL request with data and, optionally, errors.
type GraphQLHandler func(r GraphQLRequest) (data any, errors []any)

type route struct {
	method  string
	pattern *regexp.Regexp
	fn      Handler
}

type gqlRoute struct {
	pattern *regexp.Regexp
	fn      GraphQLHandler
}

// Mock serves REST under /v1, the Kraken GraphQL API at /v1/graphql/ and the
// backend GraphQL API at /backend/v1/graphql/. Account-specific REST paths need
// HTTP Basic auth with APIKey; GraphQL needs a token from obtainKrakenToken.
type Mock struct {
	Server *httptest.Server

	mu          sync.Mutex
	requests    []Request
	gqlRequests []GraphQLRequest
	routes      []route
	gqlRoutes   []gqlRoute
	tokens      map[string]bool
	refresh     map[string]bool
	counter     int
	// TokenRequests counts obtainKrakenToken calls; TokenInputs records their inputs.
	TokenRequests int
	TokenInputs   []map[string]any
	// TokenLifetime is how long new tokens last (default an hour).
	TokenLifetime time.Duration
}

// New starts a mock server. Close it with m.Server.Close().
func New() *Mock {
	m := &Mock{tokens: map[string]bool{}, refresh: map[string]bool{}, TokenLifetime: time.Hour}
	m.Server = httptest.NewServer(http.HandlerFunc(m.serve))
	return m
}

// Config returns settings pointing at the mock.
func (m *Mock) Config() config.Octopus {
	return config.Octopus{
		APIKey:            APIKey,
		APIURL:            m.Server.URL,
		GraphQLURL:        m.Server.URL + "/v1/graphql/",
		BackendGraphQLURL: m.Server.URL + "/backend/v1/graphql/",
		Timeout:           5 * time.Second,
	}
}

// On adds a REST route; pattern is a regular expression matched against the whole path relative to /v1.
func (m *Mock) On(method, pattern string, fn Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Later routes win, so tests can override earlier ones.
	m.routes = append([]route{{method, regexp.MustCompile("^" + pattern + "$"), fn}}, m.routes...)
}

// OnGraphQL adds a GraphQL handler for requests whose query text matches pattern (a regular expression).
func (m *Mock) OnGraphQL(pattern string, fn GraphQLHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gqlRoutes = append([]gqlRoute{{regexp.MustCompile(pattern), fn}}, m.gqlRoutes...)
}

// JSON is a REST handler that always returns v with status 200.
func JSON(v any) Handler {
	return func(Request, []string) (int, any) { return 200, v }
}

// Data is a GraphQL handler that always returns data without errors.
func Data(data any) GraphQLHandler {
	return func(GraphQLRequest) (any, []any) { return data, nil }
}

// Errors is a GraphQL handler that returns one error with a Kraken error code.
func Errors(message, code string) GraphQLHandler {
	return func(GraphQLRequest) (any, []any) {
		return nil, []any{map[string]any{"message": message, "extensions": map[string]any{"errorCode": code}}}
	}
}

// Requests returns the recorded REST requests.
func (m *Mock) Requests() []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Request(nil), m.requests...)
}

// Last returns the last REST request whose path contains substr, or nil.
func (m *Mock) Last(substr string) *Request {
	reqs := m.Requests()
	for i := len(reqs) - 1; i >= 0; i-- {
		if strings.Contains(reqs[i].Path, substr) {
			return &reqs[i]
		}
	}
	return nil
}

// GraphQLRequests returns the recorded GraphQL requests, excluding token requests.
func (m *Mock) GraphQLRequests() []GraphQLRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]GraphQLRequest(nil), m.gqlRequests...)
}

// LastGraphQL returns the last GraphQL request whose query contains substr, or nil.
func (m *Mock) LastGraphQL(substr string) *GraphQLRequest {
	reqs := m.GraphQLRequests()
	for i := len(reqs) - 1; i >= 0; i-- {
		if strings.Contains(reqs[i].Query, substr) {
			return &reqs[i]
		}
	}
	return nil
}

// ExpireTokens makes every issued Kraken token invalid, as if they had expired.
func (m *Mock) ExpireTokens() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens = map[string]bool{}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (m *Mock) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v1/graphql/":
		m.graphql(w, r, "main")
	case r.URL.Path == "/backend/v1/graphql/":
		m.graphql(w, r, "backend")
	case strings.HasPrefix(r.URL.Path, "/v1/"):
		m.rest(w, r)
	default:
		http.NotFound(w, r)
	}
}

// needsAuth matches the REST paths that are specific to a customer.
var needsAuth = regexp.MustCompile(`^/(accounts/|electricity-meter-points/[^/]+/meters/|gas-meter-points/)`)

func (m *Mock) rest(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1")
	user, _, _ := r.BasicAuth()
	req := Request{Method: r.Method, Path: path, Query: r.URL.Query(), BasicUser: user}
	m.mu.Lock()
	m.requests = append(m.requests, req)
	routes := append([]route(nil), m.routes...)
	m.mu.Unlock()
	if needsAuth.MatchString(path) && user != APIKey {
		writeJSON(w, 401, map[string]string{"detail": "Authentication credentials were not provided."})
		return
	}
	for _, rt := range routes {
		if rt.method != r.Method {
			continue
		}
		if match := rt.pattern.FindStringSubmatch(path); match != nil {
			status, out := rt.fn(req, match)
			writeJSON(w, status, out)
			return
		}
	}
	writeJSON(w, 404, map[string]string{"detail": "Not found."})
}

func (m *Mock) graphql(w http.ResponseWriter, r *http.Request, endpoint string) {
	body, _ := io.ReadAll(r.Body)
	var in struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&in); err != nil {
		writeJSON(w, 400, map[string]any{"errors": []any{map[string]any{"message": "bad request"}}})
		return
	}
	if strings.Contains(in.Query, "obtainKrakenToken") {
		m.token(w, in.Variables)
		return
	}
	req := GraphQLRequest{Endpoint: endpoint, Query: in.Query, Variables: in.Variables, Token: r.Header.Get("Authorization")}
	m.mu.Lock()
	m.gqlRequests = append(m.gqlRequests, req)
	valid := m.tokens[req.Token]
	routes := append([]gqlRoute(nil), m.gqlRoutes...)
	m.mu.Unlock()
	if !valid {
		writeJSON(w, 200, map[string]any{"data": nil, "errors": []any{map[string]any{
			"message": "Signature of the JWT has expired.", "extensions": map[string]any{"errorCode": "KT-CT-1124"},
		}}})
		return
	}
	for _, rt := range routes {
		if rt.pattern.MatchString(in.Query) {
			data, errs := rt.fn(req)
			out := map[string]any{"data": data}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			writeJSON(w, 200, out)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"data": nil, "errors": []any{map[string]any{"message": "mock: no handler for this query"}}})
}

func (m *Mock) token(w http.ResponseWriter, vars map[string]any) {
	input, _ := vars["input"].(map[string]any)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TokenRequests++
	m.TokenInputs = append(m.TokenInputs, input)
	key, _ := input["APIKey"].(string)
	rt, _ := input["refreshToken"].(string)
	if !(key == APIKey || (rt != "" && m.refresh[rt])) {
		writeJSON(w, 200, map[string]any{"data": map[string]any{"obtainKrakenToken": nil}, "errors": []any{map[string]any{
			"message": "Invalid data.", "extensions": map[string]any{"errorCode": "KT-CT-1135"},
		}}})
		return
	}
	m.counter++
	exp := time.Now().Add(m.TokenLifetime).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d,"n":%d}`, exp, m.counter)))
	token := "eyJhbGciOiJIUzI1NiJ9." + payload + ".sig"
	refresh := fmt.Sprintf("refresh-%d", m.counter)
	m.tokens[token], m.refresh[refresh] = true, true
	writeJSON(w, 200, map[string]any{"data": map[string]any{"obtainKrakenToken": map[string]any{
		"token": token, "refreshToken": refresh, "refreshExpiresIn": time.Now().Add(7 * 24 * time.Hour).Unix(),
	}}})
}
