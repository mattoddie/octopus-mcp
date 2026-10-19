// Package config reads octopus-mcp's settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Toolsets are groups of tools that can be switched on and off with OCTOPUS_TOOLSETS.
var Toolsets = []string{"account", "consumption", "tariffs", "intelligent", "octoplus", "raw"}

// Octopus holds the API settings.
type Octopus struct {
	// APIKey is the account's API key (from the Octopus dashboard). It is sent as the
	// REST username and exchanged for a Kraken token for GraphQL. Never log or return it.
	APIKey string
	// Account is the default account number, e.g. A-1234ABCD. Empty means discover it.
	Account string
	// APIURL is the REST base URL without /v1, e.g. https://api.octopus.energy.
	APIURL string
	// GraphQLURL is the Kraken GraphQL endpoint.
	GraphQLURL string
	// BackendGraphQLURL is Octopus Energy's own GraphQL endpoint, which serves Saving Sessions.
	BackendGraphQLURL string
	Timeout           time.Duration
}

// Tools controls which tools are registered.
type Tools struct {
	AllowWrites bool
	Toolsets    map[string]bool
}

// AllTools enables every tool, for documentation and tests.
func AllTools() Tools {
	t := Tools{AllowWrites: true, Toolsets: map[string]bool{}}
	for _, ts := range Toolsets {
		t.Toolsets[ts] = true
	}
	return t
}

// HTTP holds the Streamable HTTP transport settings.
type HTTP struct {
	Host         string
	Port         int
	AuthToken    string
	AllowedHosts []string
	// AllowUnauthenticated serves /mcp without a bearer token (MCP_ALLOW_UNAUTHENTICATED).
	AllowUnauthenticated bool
}

// MinAuthTokenLength is the shortest MCP_AUTH_TOKEN accepted.
const MinAuthTokenLength = 16

// Config is the whole configuration.
type Config struct {
	Octopus   Octopus
	Tools     Tools
	Transport string // "stdio" or "http"
	HTTP      HTTP
}

// Env looks up a variable; os.LookupEnv in production, a map in tests.
type Env func(string) (string, bool)

// MapEnv adapts a map for tests.
func MapEnv(m map[string]string) Env {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// read returns a trimmed variable, honouring the <NAME>_FILE convention for Docker secrets.
func read(env Env, name string) (string, error) {
	if file, ok := env(name + "_FILE"); ok && file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("could not read %s_FILE (%s): %w", name, file, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	v, _ := env(name)
	return strings.TrimSpace(v), nil
}

func boolValue(name, v string, fallback bool) (bool, error) {
	if v == "" {
		return fallback, nil
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("%s must be true or false, got %q", name, v)
}

func positiveInt(name, v string, fallback int) (int, error) {
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", name, v)
	}
	return n, nil
}

func checkURL(name, v string) (string, error) {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%s is not a valid URL: %s", name, v)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("%s must use https:// (or http://), got %s", name, v)
	}
	return strings.TrimRight(v, "/"), nil
}

// AccountRe matches an Octopus account number such as A-1234ABCD.
var AccountRe = regexp.MustCompile(`^[A-Z]-[0-9A-Z]{8}$`)

// Load builds the configuration from the environment.
func Load(env Env) (*Config, error) {
	var firstErr error
	get := func(name string) string {
		v, err := read(env, name)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		return v
	}
	or := func(v, fallback string) string {
		if v == "" {
			return fallback
		}
		return v
	}

	cfg := &Config{Transport: or(get("MCP_TRANSPORT"), "stdio")}
	if cfg.Transport != "stdio" && cfg.Transport != "http" {
		return nil, fmt.Errorf("MCP_TRANSPORT must be stdio or http, got %q", cfg.Transport)
	}

	var err error
	t := &cfg.Tools
	if t.AllowWrites, err = boolValue("OCTOPUS_ALLOW_WRITES", get("OCTOPUS_ALLOW_WRITES"), false); err != nil {
		return nil, err
	}
	t.Toolsets = map[string]bool{}
	for _, name := range strings.Split(or(get("OCTOPUS_TOOLSETS"), "all"), ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		switch {
		case name == "":
		case name == "all":
			for _, ts := range Toolsets {
				t.Toolsets[ts] = true
			}
		case contains(Toolsets, name):
			t.Toolsets[name] = true
		default:
			return nil, fmt.Errorf("unknown toolset %q in OCTOPUS_TOOLSETS; valid: all, %s", name, strings.Join(Toolsets, ", "))
		}
	}

	o := &cfg.Octopus
	o.APIKey = get("OCTOPUS_API_KEY")
	if firstErr != nil {
		return nil, firstErr
	}
	if o.APIKey == "" {
		return nil, fmt.Errorf("OCTOPUS_API_KEY is required: copy it from https://octopus.energy/dashboard/new/accounts/personal-details/api-access")
	}
	o.Account = strings.ToUpper(get("OCTOPUS_ACCOUNT"))
	if o.Account != "" && !AccountRe.MatchString(o.Account) {
		return nil, fmt.Errorf("OCTOPUS_ACCOUNT must be an account number like A-1234ABCD, got %q", o.Account)
	}
	if o.APIURL, err = checkURL("OCTOPUS_API_URL", or(get("OCTOPUS_API_URL"), "https://api.octopus.energy")); err != nil {
		return nil, err
	}
	o.APIURL = strings.TrimSuffix(o.APIURL, "/v1")
	if o.GraphQLURL, err = checkURL("OCTOPUS_GRAPHQL_URL", or(get("OCTOPUS_GRAPHQL_URL"), o.APIURL+"/v1/graphql/")); err != nil {
		return nil, err
	}
	if o.BackendGraphQLURL, err = checkURL("OCTOPUS_BACKEND_GRAPHQL_URL", or(get("OCTOPUS_BACKEND_GRAPHQL_URL"), "https://api.backend.octopus.energy/v1/graphql/")); err != nil {
		return nil, err
	}
	// Octopus answers /v1/graphql with a redirect; keep the trailing slash it expects.
	o.GraphQLURL += "/"
	o.BackendGraphQLURL += "/"
	ms, err := positiveInt("OCTOPUS_TIMEOUT_MS", get("OCTOPUS_TIMEOUT_MS"), 30000)
	if err != nil {
		return nil, err
	}
	o.Timeout = time.Duration(ms) * time.Millisecond

	h := &cfg.HTTP
	h.Host = or(get("MCP_HTTP_HOST"), "127.0.0.1")
	if h.Port, err = positiveInt("MCP_HTTP_PORT", get("MCP_HTTP_PORT"), 8080); err != nil {
		return nil, err
	}
	h.AuthToken = get("MCP_AUTH_TOKEN")
	for _, host := range strings.Split(get("MCP_ALLOWED_HOSTS"), ",") {
		if host = strings.TrimSpace(host); host != "" {
			h.AllowedHosts = append(h.AllowedHosts, strings.ToLower(host))
		}
	}
	if h.AllowUnauthenticated, err = boolValue("MCP_ALLOW_UNAUTHENTICATED", get("MCP_ALLOW_UNAUTHENTICATED"), false); err != nil {
		return nil, err
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if cfg.Transport == "http" {
		switch {
		// An HTTP server without a token would hand every enabled tool to anyone who can reach the port.
		case h.AuthToken == "" && !h.AllowUnauthenticated:
			return nil, errors.New("MCP_AUTH_TOKEN is required for the http transport (generate one with: openssl rand -hex 32); set MCP_ALLOW_UNAUTHENTICATED=true only behind something else that authenticates")
		case h.AuthToken != "" && len(h.AuthToken) < MinAuthTokenLength:
			return nil, fmt.Errorf("MCP_AUTH_TOKEN must be at least %d characters (generate one with: openssl rand -hex 32)", MinAuthTokenLength)
		// Without a token, the Host and Origin checks are all that stop a web page
		// from reaching the server through DNS rebinding.
		case h.AuthToken == "" && len(h.AllowedHosts) == 0:
			return nil, errors.New("MCP_ALLOW_UNAUTHENTICATED=true needs MCP_ALLOWED_HOSTS: without a token, the Host and Origin allow-list is what stops web pages reaching the server through DNS rebinding. List the host names clients use, such as octopus-mcp.example.com,localhost")
		}
	}
	return cfg, nil
}

// HealthcheckURL is the /health URL the healthcheck command checks, or "" for stdio.
// It connects to MCP_HTTP_HOST, or to loopback when the server listens on every address.
func HealthcheckURL(env Env) (string, error) {
	transport, err := read(env, "MCP_TRANSPORT")
	if err != nil || transport == "stdio" {
		return "", err
	}
	host, err := read(env, "MCP_HTTP_HOST")
	if err != nil {
		return "", err
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	p, err := read(env, "MCP_HTTP_PORT")
	if err != nil {
		return "", err
	}
	port, err := positiveInt("MCP_HTTP_PORT", p, 8080)
	if err != nil {
		return "", err
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/health", nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
