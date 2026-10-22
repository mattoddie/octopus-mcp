package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattoddie/octopus-mcp/internal/config"
	"github.com/mattoddie/octopus-mcp/internal/octopus/octopustest"
)

func readAudit(t *testing.T, path string) []Obj {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []Obj
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e Obj
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("bad audit line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func TestAuditLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	opts := config.AllTools()
	opts.AuditLog = path
	env := newEnv(t, opts)
	env.mock.OnGraphQL(`query Devices`, octopustest.Data(devicesData()))
	env.mock.OnGraphQL(`mutation UpdateBoostCharge`, octopustest.Data(Obj{"updateBoostCharge": Obj{"id": "dev-ev-1", "status": Obj{"currentState": "BOOSTING"}}}))
	env.mock.OnGraphQL(`query \{`, octopustest.Data(Obj{"ok": true}))
	env.mock.OnGraphQL(`mutation \{`, octopustest.Data(Obj{"ok": true}))

	// Reads aren't logged, nor are raw GraphQL queries.
	env.obj("octopus_list_devices", nil)
	env.obj("octopus_graphql", map[string]any{"query": `query { viewer { id } }`})
	if got := readAudit(t, path); len(got) != 0 {
		t.Fatalf("reads were logged: %v", got)
	}

	env.obj("octopus_bump_charge", map[string]any{"action": "BOOST"})
	env.callErr("octopus_bump_charge", map[string]any{"device_id": "nope"})
	env.obj("octopus_graphql", map[string]any{"query": `mutation { updateBoostCharge(input: {deviceId: $id, action: BOOST}) { id } }`, "variables": map[string]any{"id": "dev-ev-1", "input": map[string]any{"password": "hunter2"}}})
	env.callErr("octopus_graphql", map[string]any{"query": `mutation { regenerateSecretKey { key } }`})

	got := readAudit(t, path)
	if len(got) != 4 {
		t.Fatalf("want 4 entries, got %d: %v", len(got), got)
	}
	if got[0]["tool"] != "octopus_bump_charge" || got[0]["outcome"] != "ok" || get(got[0], "arguments", "action") != "BOOST" || got[0]["time"] == "" {
		t.Errorf("bump charge: %v", got[0])
	}
	if got[1]["outcome"] != "error" || !strings.Contains(got[1]["error"].(string), "isn't on account") || get(got[1], "arguments", "device_id") != "nope" {
		t.Errorf("failed call: %v", got[1])
	}
	if got[2]["tool"] != "octopus_graphql" || get(got[2], "arguments", "variables", "input", "password") != "[redacted]" || get(got[2], "arguments", "variables", "id") != "dev-ev-1" {
		t.Errorf("graphql mutation: %v", got[2])
	}
	if got[3]["outcome"] != "error" {
		t.Errorf("refused mutation: %v", got[3])
	}
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte("hunter2")) {
		t.Error("a secret reached the audit log")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != config.AuditLogMode {
		t.Errorf("mode: %v %v", info, err)
	}

	// The log is reopened for each entry, so a rotated file is recreated.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	env.obj("octopus_bump_charge", nil)
	if got := readAudit(t, path); len(got) != 1 {
		t.Errorf("after rotation: %v", got)
	}
}

func TestAuditLogStderrAndFailures(t *testing.T) {
	var buf bytes.Buffer
	l := newAuditLog(config.AuditLogStderr)
	l.stderr = &buf
	l.record("octopus_join_saving_session", joinSessionArgs{EventCode: "E1"}, errors.New("nope"))
	line := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(line, `{"time":"`) || !strings.Contains(line, `"tool":"octopus_join_saving_session"`) || !strings.HasSuffix(line, `"outcome":"error","error":"nope"}`) {
		t.Errorf("stderr line: %s", line)
	}
	if newAuditLog("") != nil {
		t.Error("an empty target made a log")
	}
	// A log that can't be written doesn't fail the call.
	bad := newAuditLog(filepath.Join(t.TempDir(), "gone", "audit.jsonl"))
	bad.record("octopus_bump_charge", nil, nil)
}
