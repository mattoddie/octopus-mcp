// Package octopus is a client for the Octopus Energy REST API and the Kraken GraphQL API.
package octopus

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/config"
)

// Error is an API error with its HTTP status.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// GraphQLError is one entry of a GraphQL response's "errors" list.
type GraphQLError struct {
	Message    string `json:"message"`
	Path       []any  `json:"path,omitempty"`
	Extensions Obj    `json:"extensions,omitempty"`
}

// Code is the Kraken error code, such as KT-CT-1111, or "".
func (e GraphQLError) Code() string { return Str(e.Extensions, "errorCode") }

// GraphQLErrors is returned when a GraphQL response has errors. Any data that
// came back alongside them has already been decoded.
type GraphQLErrors []GraphQLError

func (e GraphQLErrors) Error() string {
	var parts []string
	for _, x := range e {
		msg := strings.TrimSpace(x.Message)
		if code := x.Code(); code != "" {
			msg += " (" + code + ")"
		}
		if d := Str(x.Extensions, "errorDescription"); d != "" && !strings.Contains(msg, d) {
			msg += ": " + d
		}
		if reasons, ok := x.Extensions["boostChargeRefusalReasons"].([]any); ok && len(reasons) > 0 {
			msg += fmt.Sprintf(" Reasons: %v.", reasons)
		}
		parts = append(parts, msg)
	}
	return "Octopus GraphQL error: " + strings.Join(parts, "; ")
}

// authErrorCodes mean the Kraken token was missing, expired or rejected.
var authErrorCodes = map[string]bool{
	"KT-CT-1111": true, // Unauthorized
	"KT-CT-1112": true, // 'Authorization' header not provided
	"KT-CT-1120": true, // The Kraken Token has expired
	"KT-CT-1124": true, // JWT has expired
	"KT-CT-1139": true, // Authentication token is invalid
	"KT-CT-1143": true, // Invalid token
}

func (e GraphQLErrors) isAuth() bool {
	for _, x := range e {
		if authErrorCodes[x.Code()] {
			return true
		}
	}
	return false
}

const maxRetries = 3

// Client calls the Octopus REST API (paths relative to /v1) and the Kraken
// GraphQL API. REST requests that need it send the API key with HTTP Basic;
// GraphQL requests send a Kraken token obtained with the API key, refreshed
// before it expires and once more if Kraken rejects it.
type Client struct {
	cfg  config.Octopus
	http *http.Client
	// Now is the clock, replaceable in tests.
	Now func() time.Time

	mu         sync.Mutex
	token      string
	tokenExp   time.Time
	refresh    string
	refreshExp time.Time
	// TokenRequests counts obtainKrakenToken calls, for tests and status.
	TokenRequests int

	acctMu   sync.Mutex
	accounts []string

	// loginErr and loginFailed remember the last failed token request (see loginBackoff).
	loginErr    error
	loginFailed time.Time

	// secrets holds the current and recent Kraken tokens for redaction, readable without c.mu.
	// Old tokens stay in the list after a refresh, since they may still turn up in errors.
	secrets atomic.Pointer[[]string]
}

// New creates a client.
func New(cfg config.Octopus) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout, CheckRedirect: noRedirects},
		Now:  time.Now,
	}
}

// noRedirects returns redirects as responses instead of following them, so the
// API key and tokens never go anywhere but the configured URLs (not even to the
// same host over plain HTTP). Request URLs already carry the trailing slash
// Octopus would otherwise redirect to.
func noRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// MaxResponseBytes bounds how much of one response is read.
const MaxResponseBytes = 64 << 20

// maxRedactedSecrets bounds how many past Kraken tokens are kept for redaction.
const maxRedactedSecrets = 16

// loginBackoff is how long a rejected API key or refresh token is remembered,
// so a bad key isn't sent to Octopus again on every call.
const loginBackoff = 30 * time.Second

// Config returns the client's settings (the API key is included; never print it).
func (c *Client) Config() config.Octopus { return c.cfg }

// redact removes the API key and any Kraken token from text that may reach logs or the model.
func (c *Client) redact(s string) string {
	if c.cfg.APIKey != "" {
		s = strings.ReplaceAll(s, c.cfg.APIKey, "[redacted]")
	}
	if secrets := c.secrets.Load(); secrets != nil {
		for _, t := range *secrets {
			if len(t) > 8 {
				s = strings.ReplaceAll(s, t, "[redacted]")
			}
		}
	}
	return s
}

// redactValue redacts every string in a decoded JSON value.
func (c *Client) redactValue(v any) any {
	switch x := v.(type) {
	case string:
		return c.redact(x)
	case map[string]any:
		if x == nil {
			return x
		}
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[c.redact(k)] = c.redactValue(e)
		}
		return out
	case []any:
		if x == nil {
			return x
		}
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = c.redactValue(e)
		}
		return out
	}
	return v
}

// ---------------------------------------------------------------- REST

// Get fetches an authenticated REST path (relative to /v1) into out.
func (c *Client) Get(ctx context.Context, path string, q url.Values, out any) error {
	return c.rest(ctx, path, q, true, out)
}

// GetPublic fetches a public REST path (products, tariffs, rates) without credentials.
func (c *Client) GetPublic(ctx context.Context, path string, q url.Values, out any) error {
	return c.rest(ctx, path, q, false, out)
}

// RESTURL builds the full URL of a REST path relative to /v1.
func (c *Client) RESTURL(path string, q url.Values) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimPrefix(path, "/v1")
	if !strings.HasSuffix(path, "/") {
		path += "/" // Octopus redirects paths without the trailing slash
	}
	u := c.cfg.APIURL + "/v1" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func (c *Client) rest(ctx context.Context, path string, q url.Values, auth bool, out any) error {
	u := c.RESTURL(path, q)
	data, err := c.do(ctx, http.MethodGet, u, nil, func(r *http.Request) {
		if auth {
			r.SetBasicAuth(c.cfg.APIKey, "")
		}
	})
	if err != nil {
		return err
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("GET %s: unexpected response: %w", path, err)
	}
	return nil
}

// Page is the result of fetching a paged REST list.
type Page struct {
	Results   []Obj
	Count     int  // the API's total count
	Truncated bool // stopped at maxItems with more to come
}

// GetPages follows a paged REST list ({count, next, results}) until maxItems.
// Next links are followed on the configured host only, so credentials never go elsewhere.
func (c *Client) GetPages(ctx context.Context, path string, q url.Values, auth bool, maxItems int) (*Page, error) {
	page := &Page{}
	for pages := 0; ; pages++ {
		var res Obj
		if err := c.rest(ctx, path, q, auth, &res); err != nil {
			return nil, err
		}
		if n, ok := Num(res, "count"); ok {
			page.Count = int(n)
		}
		page.Results = append(page.Results, List(res, "results")...)
		next := Str(res, "next")
		if next == "" || len(List(res, "results")) == 0 {
			break
		}
		if len(page.Results) >= maxItems || pages >= 100 {
			page.Truncated = true
			break
		}
		nu, err := url.Parse(next)
		if err != nil {
			return nil, fmt.Errorf("bad next link from Octopus: %w", err)
		}
		path, q = nu.Path, nu.Query()
	}
	if len(page.Results) > maxItems {
		page.Results = page.Results[:maxItems]
		page.Truncated = true
	}
	return page, nil
}

// ---------------------------------------------------------------- GraphQL

// GraphQL runs an authenticated query or mutation on the Kraken API and decodes "data" into out.
func (c *Client) GraphQL(ctx context.Context, query string, vars Obj, out any) error {
	return c.graphqlAuth(ctx, c.cfg.GraphQLURL, query, vars, out)
}

// Backend runs an authenticated query or mutation on Octopus Energy's backend GraphQL API,
// which serves Saving Sessions. It accepts the same Kraken token.
func (c *Client) Backend(ctx context.Context, query string, vars Obj, out any) error {
	return c.graphqlAuth(ctx, c.cfg.BackendGraphQLURL, query, vars, out)
}

func (c *Client) graphqlAuth(ctx context.Context, endpoint, query string, vars Obj, out any) error {
	for attempt := 0; ; attempt++ {
		token, err := c.Token(ctx, attempt > 0)
		if err != nil {
			return err
		}
		err = c.graphql(ctx, endpoint, query, vars, token, out)
		var gerr GraphQLErrors
		var herr *Error
		retry := (errors.As(err, &gerr) && gerr.isAuth()) || (errors.As(err, &herr) && herr.Status == http.StatusUnauthorized)
		if retry && attempt == 0 {
			continue
		}
		return err
	}
}

func (c *Client) graphql(ctx context.Context, endpoint, query string, vars Obj, token string, out any) error {
	body := Obj{"query": query}
	if len(vars) > 0 {
		body["variables"] = vars
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	data, err := c.do(ctx, http.MethodPost, endpoint, payload, func(r *http.Request) {
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", token)
		}
	})
	if err != nil {
		return err
	}
	var res struct {
		Data   json.RawMessage `json:"data"`
		Errors GraphQLErrors   `json:"errors"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return fmt.Errorf("unexpected GraphQL response: %w", err)
	}
	if out != nil && len(res.Data) > 0 && string(res.Data) != "null" {
		dec := json.NewDecoder(bytes.NewReader(res.Data))
		dec.UseNumber()
		if err := dec.Decode(out); err != nil {
			return fmt.Errorf("unexpected GraphQL data: %w", err)
		}
	}
	if len(res.Errors) > 0 {
		// Error() quotes extensions (errorDescription, boostChargeRefusalReasons) as
		// well as the message, so scrub every string in them.
		for i := range res.Errors {
			res.Errors[i].Message = c.redact(res.Errors[i].Message)
			if ext, ok := c.redactValue(map[string]any(res.Errors[i].Extensions)).(map[string]any); ok {
				res.Errors[i].Extensions = ext
			}
			if path, ok := c.redactValue(res.Errors[i].Path).([]any); ok {
				res.Errors[i].Path = path
			}
		}
		return res.Errors
	}
	return nil
}

const tokenMutation = `mutation ObtainKrakenToken($input: ObtainJSONWebTokenInput!) {
  obtainKrakenToken(input: $input) { token refreshToken refreshExpiresIn }
}`

// Token returns a Kraken token, obtaining a new one when there is none, it is
// about to expire, or force is set (Kraken rejected the last one).
func (c *Client) Token(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.Now()
	if !force && c.token != "" && now.Before(c.tokenExp.Add(-2*time.Minute)) {
		return c.token, nil
	}
	if c.cfg.APIKey == "" {
		return "", errors.New("OCTOPUS_API_KEY is not set")
	}
	if c.loginErr != nil && now.Before(c.loginFailed.Add(loginBackoff)) {
		return "", c.loginErr
	}
	// A refresh token avoids sending the API key again; fall back to the key if it fails.
	var inputs []Obj
	if c.refresh != "" && now.Before(c.refreshExp) {
		inputs = append(inputs, Obj{"refreshToken": c.refresh})
	}
	inputs = append(inputs, Obj{"APIKey": c.cfg.APIKey})
	var lastErr error
	for _, input := range inputs {
		var res Obj
		c.TokenRequests++
		err := c.graphql(ctx, c.cfg.GraphQLURL, tokenMutation, Obj{"input": input}, "", &res)
		token := Str(res, "obtainKrakenToken", "token")
		if err == nil && token != "" {
			c.token = token
			c.tokenExp = jwtExpiry(token, now)
			c.refresh = Str(res, "obtainKrakenToken", "refreshToken")
			c.refreshExp = time.Time{}
			c.rememberSecrets(c.token, c.refresh)
			c.loginErr = nil
			if n, ok := Num(res, "obtainKrakenToken", "refreshExpiresIn"); ok {
				if n > 1e9 { // a Unix timestamp
					c.refreshExp = time.Unix(int64(n), 0)
				} else {
					c.refreshExp = now.Add(time.Duration(n) * time.Second)
				}
			}
			return token, nil
		}
		if err == nil {
			err = errors.New("no token in the response")
		}
		lastErr = err
	}
	c.token, c.refresh = "", ""
	err := fmt.Errorf("could not get a Kraken token with OCTOPUS_API_KEY (check the key is current; regenerating it on the Octopus dashboard invalidates the old one): %s", c.redact(lastErr.Error()))
	// Back off only when Octopus answered and said no; a network error may clear up straight away.
	var gerr GraphQLErrors
	var herr *Error
	if errors.As(lastErr, &gerr) || (errors.As(lastErr, &herr) && herr.Status/100 == 4) {
		c.loginErr, c.loginFailed = err, now
	}
	return "", err
}

// rememberSecrets adds newly issued tokens to the redaction list, keeping the
// most recent maxRedactedSecrets.
func (c *Client) rememberSecrets(tokens ...string) {
	var list []string
	if old := c.secrets.Load(); old != nil {
		list = append(list, *old...)
	}
	for _, t := range tokens {
		if t != "" && !slices.Contains(list, t) {
			list = append(list, t)
		}
	}
	if len(list) > maxRedactedSecrets {
		list = list[len(list)-maxRedactedSecrets:]
	}
	c.secrets.Store(&list)
}

// jwtExpiry reads the exp claim of a JWT, defaulting to 55 minutes from now.
func jwtExpiry(token string, now time.Time) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) == 3 {
		if payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "=")); err == nil {
			var claims struct {
				Exp json.Number `json:"exp"`
			}
			if json.Unmarshal(payload, &claims) == nil {
				if n, err := claims.Exp.Int64(); err == nil && n > 0 {
					return time.Unix(n, 0)
				}
			}
		}
	}
	return now.Add(55 * time.Minute)
}

// TokenStatus describes the cached Kraken token without revealing it.
func (c *Client) TokenStatus() Obj {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := Obj{"has_token": c.token != ""}
	if c.token != "" {
		out["token_expires_at"] = c.tokenExp.UTC().Format(time.RFC3339)
	}
	return out
}

// ---------------------------------------------------------------- accounts

// Accounts lists the account numbers the API key can see (cached after the first call).
func (c *Client) Accounts(ctx context.Context) ([]string, error) {
	c.acctMu.Lock()
	defer c.acctMu.Unlock()
	if c.accounts != nil {
		return c.accounts, nil
	}
	var res Obj
	if err := c.GraphQL(ctx, `query Viewer { viewer { accounts { number } } }`, nil, &res); err != nil {
		return nil, err
	}
	accounts := []string{}
	for _, a := range List(res, "viewer", "accounts") {
		if n := Str(a, "number"); n != "" {
			accounts = append(accounts, n)
		}
	}
	sort.Strings(accounts)
	c.accounts = accounts
	return accounts, nil
}

// Account picks the account number to use: the one given, OCTOPUS_ACCOUNT, or
// the only account the API key can see.
func (c *Client) Account(ctx context.Context, given string) (string, error) {
	if given = strings.ToUpper(strings.TrimSpace(given)); given != "" {
		if !config.AccountRe.MatchString(given) {
			return "", fmt.Errorf("%q is not an account number; they look like A-1234ABCD", given)
		}
		return given, nil
	}
	if c.cfg.Account != "" {
		return c.cfg.Account, nil
	}
	accounts, err := c.Accounts(ctx)
	if err != nil {
		return "", fmt.Errorf("could not discover the account number (set OCTOPUS_ACCOUNT to skip this): %w", err)
	}
	switch len(accounts) {
	case 0:
		return "", errors.New("this API key can't see any accounts; set OCTOPUS_ACCOUNT")
	case 1:
		return accounts[0], nil
	}
	return "", fmt.Errorf("this API key can see several accounts (%s); pass account_number or set OCTOPUS_ACCOUNT", strings.Join(accounts, ", "))
}

// ---------------------------------------------------------------- transport

func (c *Client) do(ctx context.Context, method, u string, payload []byte, prepare func(*http.Request)) ([]byte, error) {
	label := method + " " + displayURL(u)
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "octopus-mcp")
		prepare(req)
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%s: could not reach Octopus: %s", label, c.redact(err.Error()))
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
		resp.Body.Close()
		if len(data) > MaxResponseBytes {
			return nil, fmt.Errorf("%s: the response is larger than %d MiB; ask for a shorter period or fewer items", label, MaxResponseBytes>>20)
		}
		if resp.StatusCode/100 == 2 {
			if readErr != nil {
				return nil, fmt.Errorf("%s: reading the response: %s", label, c.redact(readErr.Error()))
			}
			return data, nil
		}
		// GraphQL reports most errors with HTTP 200, but some (auth) with 400/401 and a JSON body.
		if strings.Contains(u, "graphql") && len(data) > 0 && bytes.Contains(data, []byte(`"errors"`)) && resp.StatusCode != http.StatusTooManyRequests {
			return data, nil
		}
		// A POST (a GraphQL query or mutation) is retried only when rate-limited, which means
		// it wasn't run; a 503 might come after a mutation took effect.
		retry := resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode == http.StatusServiceUnavailable && method != http.MethodPost)
		if retry && attempt < maxRetries {
			delay := time.Duration(500<<attempt) * time.Millisecond
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
				delay = min(time.Duration(s)*time.Second, 10*time.Second)
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		return nil, &Error{
			Status:  resp.StatusCode,
			Message: c.redact(fmt.Sprintf("%s failed (HTTP %d): %s.%s", label, resp.StatusCode, errorMessage(resp.StatusCode, data), hint(resp.StatusCode, u))),
		}
	}
}

// displayURL drops the scheme and host so errors stay short, keeping the path and query.
func displayURL(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	s := p.Path
	if p.RawQuery != "" {
		s += "?" + p.RawQuery
	}
	return s
}

// errorMessage pulls a readable message out of an Octopus (Django REST framework) error body.
func errorMessage(status int, data []byte) string {
	var j any
	if json.Unmarshal(data, &j) != nil {
		s := strings.TrimSpace(string(data))
		if s == "" || strings.HasPrefix(s, "<") {
			return http.StatusText(status)
		}
		return truncate(s, 300)
	}
	switch v := j.(type) {
	case map[string]any:
		if d, ok := v["detail"].(string); ok && d != "" {
			return strings.TrimRight(d, ".")
		}
		var parts []string
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s: %v", k, v[k]))
		}
		return truncate(strings.Join(parts, "; "), 500)
	case []any:
		return truncate(fmt.Sprint(v...), 500)
	}
	return truncate(string(data), 300)
}

func hint(status int, u string) string {
	switch {
	case status/100 == 3:
		return " Octopus redirected the request, and redirects aren't followed so that credentials only go to the configured URLs. Check OCTOPUS_API_URL, OCTOPUS_GRAPHQL_URL and OCTOPUS_BACKEND_GRAPHQL_URL."
	case status == http.StatusUnauthorized:
		return " Octopus rejected the API key. Check OCTOPUS_API_KEY is the current key from your Octopus dashboard (Personal details → API access)."
	case status == http.StatusForbidden || (status == http.StatusNotFound && strings.Contains(u, "/v1/accounts/")):
		return " The API key may not have access to this account. Check OCTOPUS_ACCOUNT or the account_number argument."
	case status == http.StatusNotFound && strings.Contains(u, "-meter-points/"):
		return " Check the MPAN/MPRN and meter serial number with octopus_get_account."
	case status == http.StatusNotFound && strings.Contains(u, "/v1/products/"):
		return " Check the product and tariff codes with octopus_list_products and octopus_get_product."
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
