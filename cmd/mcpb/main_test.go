package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
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

// server.json (the MCP Registry entry) must describe the same server as the
// manifest: the same settings, as environment variables for the Docker image,
// and one bundle for each platform `make mcpb` builds.
func TestServerJSONMatchesManifest(t *testing.T) {
	data, err := os.ReadFile("../../server.json")
	if os.IsNotExist(err) {
		t.Skip("server.json is not available")
	}
	if err != nil {
		t.Fatal(err)
	}
	type envVar struct {
		Name       string  `json:"name"`
		Value      *string `json:"value"`
		Default    *string `json:"default"`
		Format     string  `json:"format"`
		IsRequired bool    `json:"isRequired"`
		IsSecret   bool    `json:"isSecret"`
	}
	var server struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		WebsiteURL string `json:"websiteUrl"`
		Repository struct {
			URL string `json:"url"`
		} `json:"repository"`
		Packages []struct {
			RegistryType string   `json:"registryType"`
			Identifier   string   `json:"identifier"`
			Env          []envVar `json:"environmentVariables"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &server); err != nil {
		t.Fatalf("server.json: %v", err)
	}

	raw, err := readManifest(t).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		Homepage   string `json:"homepage"`
		Repository struct {
			URL string `json:"url"`
		} `json:"repository"`
		UserConfig map[string]struct {
			Type      string `json:"type"`
			Default   any    `json:"default"`
			Required  bool   `json:"required"`
			Sensitive bool   `json:"sensitive"`
		} `json:"user_config"`
		Server struct {
			MCPConfig struct {
				Env map[string]string `json:"env"`
			} `json:"mcp_config"`
		} `json:"server"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}

	if want := "io.github.mattoddie/" + manifest.Name; server.Name != want {
		t.Errorf("name %q, want %q", server.Name, want)
	}
	if server.Version != manifest.Version {
		t.Errorf("version %q, want the manifest's %q (the release job stamps the real one)", server.Version, manifest.Version)
	}
	if server.Repository.URL != manifest.Repository.URL {
		t.Errorf("repository %q, want %q", server.Repository.URL, manifest.Repository.URL)
	}
	if server.WebsiteURL != manifest.Homepage {
		t.Errorf("websiteUrl %q, want %q", server.WebsiteURL, manifest.Homepage)
	}

	docs, err := os.ReadFile("../../docs/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	makefile, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	var platforms []string
	for _, line := range strings.Split(string(makefile), "\n") {
		if rest, ok := strings.CutPrefix(line, "MCPB_PLATFORMS :="); ok {
			for _, p := range strings.Fields(rest) {
				platforms = append(platforms, strings.ReplaceAll(p, "/", "-"))
			}
		}
	}
	if len(platforms) == 0 {
		t.Fatal("MCPB_PLATFORMS not found in the Makefile")
	}

	var oci, bundles []string
	for _, p := range server.Packages {
		switch p.RegistryType {
		case "oci":
			oci = append(oci, p.Identifier)
			if want := "ghcr.io/mattoddie/" + manifest.Name + ":" + server.Version; p.Identifier != want {
				t.Errorf("image %q, want %q", p.Identifier, want)
			}
			seen := map[string]bool{}
			for _, v := range p.Env {
				seen[v.Name] = true
				if !strings.Contains(string(docs), "`"+v.Name+"`") {
					t.Errorf("%s isn't documented in docs/configuration.md", v.Name)
				}
				ref, ok := manifest.Server.MCPConfig.Env[v.Name]
				if !ok {
					t.Errorf("%s is in server.json but not in the manifest's env", v.Name)
					continue
				}
				m := userConfigRef.FindStringSubmatch(ref)
				if m == nil {
					// A fixed value, like MCP_TRANSPORT=stdio.
					if v.Value == nil || *v.Value != ref {
						t.Errorf("%s: want the fixed value %q, as in the manifest", v.Name, ref)
					}
					continue
				}
				if v.Value != nil {
					t.Errorf("%s has a fixed value, but the manifest lets the user set it", v.Name)
				}
				setting, ok := manifest.UserConfig[m[1]]
				if !ok {
					t.Errorf("%s: user_config.%s is missing", v.Name, m[1])
					continue
				}
				if v.IsRequired != setting.Required || v.IsSecret != setting.Sensitive {
					t.Errorf("%s: isRequired %v, isSecret %v; the manifest says required %v, sensitive %v",
						v.Name, v.IsRequired, v.IsSecret, setting.Required, setting.Sensitive)
				}
				if (v.Format == "boolean") != (setting.Type == "boolean") {
					t.Errorf("%s: format %q, but the setting is a %s", v.Name, v.Format, setting.Type)
				}
				want := fmt.Sprint(setting.Default)
				if setting.Default == nil || want == "" {
					if v.Default != nil {
						t.Errorf("%s: default %q, but the manifest has none", v.Name, *v.Default)
					}
				} else if v.Default == nil || *v.Default != want {
					t.Errorf("%s: want default %q, as in the manifest", v.Name, want)
				}
			}
			for name := range manifest.Server.MCPConfig.Env {
				if !seen[name] {
					t.Errorf("%s is in the manifest's env but not in server.json", name)
				}
			}
		case "mcpb":
			bundles = append(bundles, p.Identifier)
		default:
			t.Errorf("unexpected package type %q", p.RegistryType)
		}
	}
	if len(oci) != 1 {
		t.Errorf("want one OCI package, got %v", oci)
	}
	var want []string
	for _, p := range platforms {
		want = append(want, "https://github.com/mattoddie/"+manifest.Name+"/releases/download/v"+server.Version+"/"+manifest.Name+"-"+server.Version+"-"+p+".mcpb")
	}
	if !reflect.DeepEqual(bundles, want) {
		t.Errorf("bundles:\n%s\nwant one for each of MCPB_PLATFORMS:\n%s", strings.Join(bundles, "\n"), strings.Join(want, "\n"))
	}
}
