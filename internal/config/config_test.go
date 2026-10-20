package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const key = "sk_live_example"

func TestDefaults(t *testing.T) {
	cfg, err := Load(MapEnv(map[string]string{"OCTOPUS_API_KEY": key}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Transport != "stdio" || cfg.HTTP.Port != 8080 || cfg.HTTP.Host != "127.0.0.1" {
		t.Errorf("transport defaults: %+v %+v", cfg.Transport, cfg.HTTP)
	}
	o := cfg.Octopus
	if o.APIURL != "https://api.octopus.energy" || o.GraphQLURL != "https://api.octopus.energy/v1/graphql/" || o.BackendGraphQLURL != "https://api.backend.octopus.energy/v1/graphql/" {
		t.Errorf("URLs: %s %s %s", o.APIURL, o.GraphQLURL, o.BackendGraphQLURL)
	}
	if o.Timeout != 30*time.Second || o.Account != "" || o.APIKey != key {
		t.Errorf("octopus: %+v", o)
	}
	if cfg.Tools.AllowWrites || len(cfg.Tools.Toolsets) != len(Toolsets) {
		t.Errorf("tools: %+v", cfg.Tools)
	}
}

func TestOverrides(t *testing.T) {
	cfg, err := Load(MapEnv(map[string]string{
		"OCTOPUS_API_KEY":      key,
		"OCTOPUS_ACCOUNT":      " a-1234abcd ",
		"OCTOPUS_ALLOW_WRITES": "yes",
		"OCTOPUS_API_URL":      "https://api.oegb-kraken.energy/v1/",
		"OCTOPUS_TIMEOUT_MS":   "5000",
		"MCP_TRANSPORT":        "http",
		"MCP_HTTP_PORT":        "9000",
		"MCP_AUTH_TOKEN":       "secret-token-0123456789",
	}))
	if err != nil {
		t.Fatal(err)
	}
	o := cfg.Octopus
	if o.Account != "A-1234ABCD" || o.APIURL != "https://api.oegb-kraken.energy" || o.GraphQLURL != "https://api.oegb-kraken.energy/v1/graphql/" || o.Timeout != 5*time.Second {
		t.Errorf("octopus: %+v", o)
	}
	if !cfg.Tools.AllowWrites || cfg.Transport != "http" || cfg.HTTP.Port != 9000 {
		t.Errorf("settings: %+v %+v", cfg.Tools, cfg.HTTP)
	}
}

func TestValidation(t *testing.T) {
	for env, want := range map[string]string{
		"":                              "OCTOPUS_API_KEY is required",
		"OCTOPUS_ALLOW_WRITES=maybe":    "must be true or false",
		"MCP_TRANSPORT=sse":             "stdio or http",
		"OCTOPUS_TOOLSETS=tariffs,nope": `unknown toolset "nope"`,
		"OCTOPUS_API_URL=ftp://x":       "https://",
		"OCTOPUS_API_URL=http://api.octopus.energy":       "only allowed for localhost",
		"OCTOPUS_GRAPHQL_URL=http://example.com/graphql/": "only allowed for localhost",
		"OCTOPUS_API_URL=https://user@api.example.com":    "must not contain a user name",
		"OCTOPUS_TIMEOUT_MS=-1":                           "positive integer",
		"MCP_HTTP_PORT=http":                              "positive integer",
		"OCTOPUS_ACCOUNT=12345":                           "account number like A-1234ABCD",
		"OCTOPUS_API_KEY_FILE=/nonexistent":               "OCTOPUS_API_KEY_FILE",
	} {
		m := map[string]string{}
		if env != "" {
			k, v, _ := strings.Cut(env, "=")
			m[k] = v
		}
		if !strings.HasPrefix(env, "OCTOPUS_API_KEY") && env != "" {
			m["OCTOPUS_API_KEY"] = key
		}
		_, err := Load(MapEnv(m))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", env, err, want)
		}
		if err != nil && strings.Contains(err.Error(), key) {
			t.Errorf("%s: error leaks the API key: %v", env, err)
		}
	}
}

func TestToolsetsAndSecretsFromFiles(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "key")
	if err := os.WriteFile(secret, []byte(key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(MapEnv(map[string]string{
		"OCTOPUS_TOOLSETS":     " Tariffs, consumption ",
		"OCTOPUS_API_KEY_FILE": secret,
		"OCTOPUS_API_KEY":      "ignored-when-the-file-is-set",
		"MCP_ALLOWED_HOSTS":    "MCP.example.com, localhost:8080",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tools.Toolsets) != 2 || !cfg.Tools.Toolsets["tariffs"] || !cfg.Tools.Toolsets["consumption"] {
		t.Errorf("toolsets: %v", cfg.Tools.Toolsets)
	}
	if cfg.Octopus.APIKey != key {
		t.Errorf("key from file: %q", cfg.Octopus.APIKey)
	}
	if strings.Join(cfg.HTTP.AllowedHosts, ",") != "mcp.example.com,localhost:8080" {
		t.Errorf("allowed hosts: %v", cfg.HTTP.AllowedHosts)
	}
}

func TestHTTPNeedsAuthToken(t *testing.T) {
	base := map[string]string{"OCTOPUS_API_KEY": key, "MCP_TRANSPORT": "http"}
	if _, err := Load(MapEnv(base)); err == nil || !strings.Contains(err.Error(), "MCP_AUTH_TOKEN is required") {
		t.Fatalf("http without a token: got %v", err)
	}
	base["MCP_ALLOW_UNAUTHENTICATED"] = "true"
	base["MCP_ALLOWED_HOSTS"] = "localhost"
	if _, err := Load(MapEnv(base)); err != nil {
		t.Fatalf("explicit opt-out: %v", err)
	}
	delete(base, "MCP_ALLOW_UNAUTHENTICATED")
	delete(base, "MCP_ALLOWED_HOSTS")
	base["MCP_AUTH_TOKEN"] = "secret-token-0123456789"
	if _, err := Load(MapEnv(base)); err != nil {
		t.Fatalf("with a token: %v", err)
	}
}

func TestHTTPTokenAndUnauthenticatedRules(t *testing.T) {
	load := func(kv ...string) error {
		m := map[string]string{"OCTOPUS_API_KEY": key, "MCP_TRANSPORT": "http"}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		_, err := Load(MapEnv(m))
		return err
	}
	if err := load("MCP_AUTH_TOKEN", "short"); err == nil || !strings.Contains(err.Error(), "at least 16 characters") {
		t.Errorf("short token: %v", err)
	}
	if err := load("MCP_AUTH_TOKEN", "fifteen-chars-x"); err == nil {
		t.Error("15-character token accepted")
	}
	if err := load("MCP_AUTH_TOKEN", "sixteen-chars-xx"); err != nil {
		t.Errorf("16-character token: %v", err)
	}
	// Short tokens don't matter for stdio, where the token isn't used.
	if err := load("MCP_TRANSPORT", "stdio", "MCP_AUTH_TOKEN", "x"); err != nil {
		t.Errorf("stdio: %v", err)
	}
	if err := load("MCP_ALLOW_UNAUTHENTICATED", "true"); err == nil || !strings.Contains(err.Error(), "MCP_ALLOWED_HOSTS") {
		t.Errorf("unauthenticated without an allow-list: %v", err)
	}
	cfg, err := Load(MapEnv(map[string]string{"OCTOPUS_API_KEY": key, "MCP_TRANSPORT": "http", "MCP_ALLOW_UNAUTHENTICATED": "true", "MCP_ALLOWED_HOSTS": "localhost"}))
	if err != nil || !cfg.HTTP.AllowUnauthenticated {
		t.Errorf("unauthenticated with an allow-list: %v", err)
	}
}

func TestHealthcheckURL(t *testing.T) {
	for env, want := range map[string]string{
		"":                          "http://127.0.0.1:8080/health",
		"MCP_TRANSPORT=stdio":       "",
		"MCP_HTTP_HOST=0.0.0.0":     "http://127.0.0.1:8080/health",
		"MCP_HTTP_HOST=::":          "http://127.0.0.1:8080/health",
		"MCP_HTTP_HOST=192.168.1.5": "http://192.168.1.5:8080/health",
		"MCP_HTTP_HOST=::1":         "http://[::1]:8080/health",
		"MCP_HTTP_HOST=[fd00::5]":   "http://[fd00::5]:8080/health",
		"MCP_HTTP_PORT=9090":        "http://127.0.0.1:9090/health",
	} {
		m := map[string]string{}
		if env != "" {
			k, v, _ := strings.Cut(env, "=")
			m[k] = v
		}
		got, err := HealthcheckURL(MapEnv(m))
		if err != nil || got != want {
			t.Errorf("%s: %q %v, want %q", env, got, err, want)
		}
	}
	if _, err := HealthcheckURL(MapEnv(map[string]string{"MCP_HTTP_PORT": "x"})); err == nil {
		t.Error("bad port accepted")
	}
}

func TestLoopbackURLsMayUseHTTP(t *testing.T) {
	for _, u := range []string{"http://localhost:8000", "http://127.0.0.1:8000", "http://[::1]:8000", "https://api.example.com"} {
		if _, err := Load(MapEnv(map[string]string{"OCTOPUS_API_KEY": key, "OCTOPUS_API_URL": u})); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	_, err := Load(MapEnv(map[string]string{"OCTOPUS_API_KEY": key, "OCTOPUS_API_URL": "https://user:" + "hunter2" + "@api.example.com"}))
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("userinfo: %v", err)
	}
}
