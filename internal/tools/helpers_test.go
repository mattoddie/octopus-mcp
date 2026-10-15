package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/octopus-mcp/internal/config"
	"github.com/mattoddie/octopus-mcp/internal/octopus"
	"github.com/mattoddie/octopus-mcp/internal/octopus/octopustest"
)

// testNow is the fixed clock for tool tests: 13:00 UK time (BST) on Thursday 15 October 2026.
var testNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

type testEnv struct {
	t       *testing.T
	mock    *octopustest.Mock
	client  *octopus.Client
	session *mcp.ClientSession
}

// newEnv starts a mock Octopus and an MCP session with the given tool options (all tools by default).
func newEnv(t *testing.T, opts ...config.Tools) *testEnv {
	t.Helper()
	m := octopustest.New()
	t.Cleanup(m.Server.Close)
	cfg := m.Config()
	cfg.Account = octopustest.Account
	client := octopus.New(cfg)
	client.Now = func() time.Time { return testNow }
	o := config.AllTools()
	if len(opts) > 0 {
		o = opts[0]
	}
	return &testEnv{t: t, mock: m, client: client, session: connect(t, client, o)}
}

func connect(t *testing.T, client *octopus.Client, o config.Tools) *mcp.ClientSession {
	t.Helper()
	server := NewServer(client, o, nil)
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func (e *testEnv) raw(name string, args map[string]any) *mcp.CallToolResult {
	e.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	res, err := e.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// obj runs a tool and decodes its JSON object result, failing on a tool error.
func (e *testEnv) obj(name string, args map[string]any) Obj {
	e.t.Helper()
	res := e.raw(name, args)
	if res.IsError {
		e.t.Fatalf("%s returned an error: %s", name, text(res))
	}
	var out Obj
	dec := json.NewDecoder(strings.NewReader(text(res)))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		e.t.Fatalf("%s: bad JSON %q: %v", name, text(res), err)
	}
	return out
}

// callErr runs a tool that should fail and returns its error text.
func (e *testEnv) callErr(name string, args map[string]any) string {
	e.t.Helper()
	res := e.raw(name, args)
	if !res.IsError {
		e.t.Fatalf("%s: expected an error, got %s", name, text(res))
	}
	return text(res)
}

func asJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func num(v any) float64 {
	f, _ := octopus.Num(Obj{"v": v}, "v")
	return f
}

func toolNames(t *testing.T, session *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	out := map[string]*mcp.Tool{}
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		out[tool.Name] = tool
	}
	return out
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func get(v any, path ...string) any { return octopus.Get(v, path...) }

// ---------------------------------------------------------------- fixtures

const accountFixture = `{
  "number": "A-1234ABCD",
  "properties": [
    {
      "id": 1001, "moved_in_at": "2024-01-01T00:00:00Z", "moved_out_at": null,
      "address_line_1": "1 Example Street", "address_line_2": "", "town": "Exampleton", "county": "", "postcode": "EX1 1AA",
      "electricity_meter_points": [
        {
          "mpan": "1000000000001", "profile_class": 1, "consumption_standard": 2900, "is_export": false,
          "meters": [{"serial_number": "21E0000001", "registers": [{"identifier": "1", "rate": "STANDARD", "is_settlement_register": true}]}],
          "agreements": [
            {"tariff_code": "E-1R-VAR-22-11-01-C", "valid_from": "2024-01-01T00:00:00Z", "valid_to": "2026-10-01T00:00:00+01:00"},
            {"tariff_code": "E-1R-AGILE-24-10-01-C", "valid_from": "2026-10-01T00:00:00+01:00", "valid_to": null}
          ]
        },
        {
          "mpan": "1000000000002", "profile_class": 1, "consumption_standard": 0, "is_export": true,
          "meters": [{"serial_number": "21E0000001"}],
          "agreements": [{"tariff_code": "E-1R-OUTGOING-VAR-24-10-26-C", "valid_from": "2024-06-01T00:00:00Z", "valid_to": null}]
        }
      ],
      "gas_meter_points": [
        {
          "mprn": "3000000001", "consumption_standard": 11500,
          "meters": [{"serial_number": "G4A0000001"}],
          "agreements": [{"tariff_code": "G-1R-VAR-22-11-01-C", "valid_from": "2024-01-01T00:00:00Z", "valid_to": null}]
        }
      ]
    },
    {
      "id": 900, "moved_in_at": "2020-01-01T00:00:00Z", "moved_out_at": "2023-12-31T00:00:00Z",
      "address_line_1": "9 Old Road", "town": "Formerton", "postcode": "EX9 9ZZ",
      "electricity_meter_points": [], "gas_meter_points": []
    }
  ]
}`

func meterDevicesData() Obj {
	return Obj{"account": Obj{
		"electricityAgreements": []any{Obj{
			"tariff":     Obj{"displayName": "Agile Octopus", "tariffCode": "E-1R-AGILE-24-10-01-C"},
			"meterPoint": Obj{"mpan": "1000000000001", "meters": []any{Obj{"serialNumber": "21E0000001", "meterType": "S2ADE", "smartImportElectricityMeter": Obj{"deviceId": "00-11-22-33-44-55-66-77"}, "smartExportElectricityMeter": Obj{"deviceId": "00-11-22-33-44-55-66-78"}}}},
		}},
		"gasAgreements": []any{Obj{
			"tariff":     Obj{"displayName": "Flexible Octopus", "tariffCode": "G-1R-VAR-22-11-01-C"},
			"meterPoint": Obj{"mprn": "3000000001", "meters": []any{Obj{"serialNumber": "G4A0000001", "consumptionUnits": "m³", "smartGasMeter": Obj{"deviceId": "00-11-22-33-44-55-66-99"}}}},
		}},
	}}
}

// withAccount registers the account fixture on the mock, over REST and GraphQL.
func (e *testEnv) withAccount() {
	e.mock.On("GET", "/accounts/A-1234ABCD/", octopustest.JSON(jsonObj(e.t, accountFixture)))
	e.mock.OnGraphQL(`query AccountMeters`, octopustest.Data(meterDevicesData()))
}

func jsonObj(t *testing.T, s string) Obj {
	t.Helper()
	var o Obj
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&o); err != nil {
		t.Fatal(err)
	}
	return o
}

// rateRecord is a REST unit rate or standing charge record.
func rateRecord(from, to time.Time, incVAT float64) Obj {
	r := Obj{"value_exc_vat": incVAT / 1.05, "value_inc_vat": incVAT, "valid_from": from.UTC().Format(time.RFC3339), "valid_to": nil, "payment_method": nil}
	if !to.IsZero() {
		r["valid_to"] = to.UTC().Format(time.RFC3339)
	}
	return r
}

// paged wraps records in the REST list shape, newest first as Octopus returns them.
func paged(records ...Obj) Obj {
	rs := make([]any, len(records))
	for i, r := range records {
		rs[len(records)-1-i] = r
	}
	return Obj{"count": len(records), "next": nil, "previous": nil, "results": rs}
}
