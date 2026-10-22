package main

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, file, body string) string {
	t.Helper()
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

// readManifest reads the committed manifest. The Docker build only copies the
// Go sources, so tests that need it skip there; CI runs them outside Docker.
func readManifest(t *testing.T) *object {
	t.Helper()
	data, err := os.ReadFile("../../mcpb/manifest.json")
	if os.IsNotExist(err) {
		t.Skip("mcpb/manifest.json is not available")
	}
	if err != nil {
		t.Fatal(err)
	}
	m, err := parseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPack(t *testing.T) {
	dir := t.TempDir()
	bin := write(t, filepath.Join(dir, "octopus-mcp.exe"), "binary")
	license := write(t, filepath.Join(dir, "LICENSE"), "MIT")
	write(t, filepath.Join(dir, "icon.png"), "png")
	write(t, filepath.Join(dir, "manifest.json"), `{"manifest_version":"0.3","name":"octopus-mcp","version":"0.0.0-dev","icon":"icon.png",
		"server":{"type":"binary","entry_point":"server/octopus-mcp"},"compatibility":{"platforms":["darwin","win32","linux"]}}`)
	out := filepath.Join(dir, "out", "x.mcpb")
	if err := pack("1.2.3-rc.1", "windows", bin, out, dir, license); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		names = append(names, f.Name)
		files[f.Name] = f
	}
	if want := []string{"manifest.json", "icon.png", "LICENSE", "server/octopus-mcp.exe"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("entries: %v, want %v", names, want)
	}
	if mode := files["server/octopus-mcp.exe"].Mode().Perm(); mode != 0o755 {
		t.Errorf("binary mode %v", mode)
	}
	r, _ := files["manifest.json"].Open()
	data, _ := io.ReadAll(r)
	var m struct {
		ManifestVersion string `json:"manifest_version"`
		Version         string `json:"version"`
		Compatibility   struct {
			Platforms []string `json:"platforms"`
		} `json:"compatibility"`
		Server struct {
			EntryPoint string `json:"entry_point"`
		} `json:"server"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.Version != "1.2.3-rc.1" || !reflect.DeepEqual(m.Compatibility.Platforms, []string{"win32"}) || m.Server.EntryPoint != "server/octopus-mcp.exe" {
		t.Errorf("stamped manifest: %+v", m)
	}
	// Keys keep their order.
	if !strings.HasPrefix(string(data), "{\n  \"manifest_version\": \"0.3\",\n  \"name\": \"octopus-mcp\",\n  \"version\": \"1.2.3-rc.1\",") {
		t.Errorf("key order: %s", data)
	}

	for _, bad := range [][2]string{{"v1.2.3", "linux"}, {"1.2", "linux"}, {"1.2.3", "plan9"}} {
		if err := pack(bad[0], bad[1], bin, out, dir, license); err == nil {
			t.Errorf("pack(%q, %q) succeeded", bad[0], bad[1])
		}
	}
}

// The manifest's settings, at their defaults, must start the server read-only:
// every variable it sets comes from a user_config entry that exists.
func TestDefaultEnv(t *testing.T) {
	m := readManifest(t)
	env, err := defaultEnv(m)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"MCP_TRANSPORT": "stdio", "OCTOPUS_API_KEY": samples["api_key"], "OCTOPUS_ALLOW_WRITES": "false", "OCTOPUS_ACCOUNT": "", "OCTOPUS_TOOLSETS": "", "OCTOPUS_AUDIT_LOG": ""} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
	if err := m.set("user_config", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := defaultEnv(m); err == nil {
		t.Error("a reference to a missing user_config entry was accepted")
	}
}

func TestObjectRoundTrip(t *testing.T) {
	src := "{\n  \"b\": 1,\n  \"a\": {\n    \"z\": \"→ <x> & y\",\n    \"y\": []\n  }\n}\n"
	o, err := parseObject([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := o.format(); got != src {
		t.Errorf("round trip:\n%s\nwant:\n%s", got, src)
	}
	if err := o.set("c", []string{"<"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := o.format(); !strings.HasSuffix(got, "\"c\": [\n    \"<\"\n  ]\n}\n") {
		t.Errorf("set: %s", got)
	}
	for _, bad := range []string{`[]`, `{"a":1,"a":2}`, `{"a":1} {}`, `{"a":`} {
		if _, err := parseObject([]byte(bad)); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}
