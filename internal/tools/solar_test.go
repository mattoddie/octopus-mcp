package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
	"github.com/mattoddie/octopus-mcp/internal/octopus/octopustest"
)

func TestSolarSummary(t *testing.T) {
	env := newEnv(t)
	env.withAccount()
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, octopus.London)
	noon := day.Add(12 * time.Hour)
	// Import: 0.5 kWh every half-hour (24 kWh) on Agile at 10p then 20p: £3.60, plus 50p standing.
	env.mock.On("GET", "/electricity-meter-points/1000000000001/meters/21E0000001/consumption/", octopustest.JSON(Obj{"count": 48, "next": nil, "results": halfHours(day, 48, 0.5)}))
	env.mock.On("GET", "/products/AGILE-24-10-01/electricity-tariffs/E-1R-AGILE-24-10-01-C/standard-unit-rates/", octopustest.JSON(paged(
		rateRecord(day.Add(-time.Hour), noon, 10), rateRecord(noon, day.Add(25*time.Hour), 20))))
	env.mock.On("GET", "/products/AGILE-24-10-01/electricity-tariffs/E-1R-AGILE-24-10-01-C/standing-charges/", octopustest.JSON(paged(
		rateRecord(time.Date(2024, 9, 30, 23, 0, 0, 0, time.UTC), time.Time{}, 50))))
	// Export: 2 kWh in each of 4 half-hours from noon (8 kWh) at 15p: £1.20.
	env.mock.On("GET", "/electricity-meter-points/1000000000002/meters/21E0000001/consumption/", octopustest.JSON(Obj{"count": 4, "next": nil, "results": halfHours(noon, 4, 2)}))
	env.mock.On("GET", "/products/OUTGOING-VAR-24-10-26/electricity-tariffs/E-1R-OUTGOING-VAR-24-10-26-C/standard-unit-rates/", octopustest.JSON(paged(rateRecord(day.AddDate(-1, 0, 0), time.Time{}, 15))))

	res := env.obj("octopus_get_solar_summary", map[string]any{"from": "2026-10-13", "to": "2026-10-13", "generated_kwh": 20})
	s := res["summary"].(Obj)
	if num(s["import_kwh"]) != 24 || num(s["export_kwh"]) != 8 || num(s["net_import_kwh"]) != 16 || num(s["import_unit_cost_gbp"]) != 3.6 || num(s["export_earnings_gbp"]) != 1.2 || num(s["standing_charge_gbp"]) != 0.5 || num(s["net_cost_gbp"]) != 2.9 {
		t.Errorf("summary: %v", s)
	}
	if num(s["avg_import_rate_p_per_kwh"]) != 15 || num(s["avg_export_rate_p_per_kwh"]) != 15 || num(s["half_hours_exporting"]) != 4 {
		t.Errorf("rates: %v", s)
	}
	// 20 kWh generated, 8 exported: 12 used at home, 60%; home use 36 kWh, a third from solar.
	sol := s["solar"].(Obj)
	if num(sol["self_consumed_kwh"]) != 12 || num(sol["self_consumption_percent"]) != 60 || num(sol["home_use_kwh"]) != 36 || num(sol["self_sufficiency_percent"]) != 33.3 || num(sol["self_consumption_saving_gbp"]) != 1.8 {
		t.Errorf("solar: %v", sol)
	}
	rows := list(res["rows"])
	if len(rows) != 1 || num(get(rows[0], "net_unit_cost_gbp")) != 2.4 || get(rows[0], "start") != "2026-10-13" {
		t.Errorf("rows: %v", rows)
	}
	if get(res, "export_meter", "meter_point") != "1000000000002" {
		t.Errorf("export meter: %v", res["export_meter"])
	}

	// Without generation data, self-consumption is explained rather than guessed.
	res = env.obj("octopus_get_solar_summary", map[string]any{"from": "2026-10-13", "to": "2026-10-13", "group_by": "total"})
	if get(res, "summary", "solar") != nil || res["rows"] != nil || !strings.Contains(asJSON(t, res["notes"]), "generated_kwh") {
		t.Errorf("no generation: %v", res)
	}
	res = env.obj("octopus_get_solar_summary", map[string]any{"from": "2026-10-13", "to": "2026-10-13", "generated_kwh": 5})
	if get(res, "summary", "solar") != nil || !strings.Contains(asJSON(t, res["notes"]), "less than what was exported") {
		t.Errorf("generation below export: %v", res)
	}
}

func TestSolarSummaryNeedsAnExportMeter(t *testing.T) {
	env := newEnv(t)
	acct := jsonObj(t, accountFixture)
	p := list(acct["properties"])[0].(Obj)
	p["electricity_meter_points"] = list(p["electricity_meter_points"])[:1]
	env.mock.On("GET", "/accounts/A-1234ABCD/", octopustest.JSON(acct))
	if msg := env.callErr("octopus_get_solar_summary", nil); !strings.Contains(msg, "needs an export meter") {
		t.Errorf("no export meter: %s", msg)
	}
}
