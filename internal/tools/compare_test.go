package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
	"github.com/mattoddie/octopus-mcp/internal/octopus/octopustest"
)

func TestCompareTariffs(t *testing.T) {
	env := newEnv(t)
	env.withAccount()
	// 13 October 2026 (UK time): 1 kWh every half-hour, on Agile.
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, octopus.London)
	noon := day.Add(12 * time.Hour)
	env.mock.On("GET", "/electricity-meter-points/1000000000001/meters/21E0000001/consumption/", octopustest.JSON(Obj{"count": 48, "next": nil, "results": halfHours(day, 48, 1)}))
	// Current tariff, Agile: 10p until noon, 30p after: 24×10 + 24×30 = £9.60, standing 50p.
	env.mock.On("GET", "/products/AGILE-24-10-01/electricity-tariffs/E-1R-AGILE-24-10-01-C/standard-unit-rates/", octopustest.JSON(paged(
		rateRecord(day.Add(-time.Hour), noon, 10), rateRecord(noon, day.Add(25*time.Hour), 30))))
	env.mock.On("GET", "/products/AGILE-24-10-01/electricity-tariffs/E-1R-AGILE-24-10-01-C/standing-charges/", octopustest.JSON(paged(
		rateRecord(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), time.Time{}, 50))))
	// A flat tariff at 15p (£7.20) with a 60p standing charge: £7.80, £2.30 cheaper.
	env.mock.On("GET", "/products/FIX-12M-26-01-01/electricity-tariffs/E-1R-FIX-12M-26-01-01-C/standard-unit-rates/", octopustest.JSON(paged(
		rateRecord(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Time{}, 15))))
	env.mock.On("GET", "/products/FIX-12M-26-01-01/electricity-tariffs/E-1R-FIX-12M-26-01-01-C/standing-charges/", octopustest.JSON(paged(
		rateRecord(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Time{}, 60))))
	// A product that started halfway through the day can only price part of it.
	env.mock.On("GET", "/products/NEW-26-10-13/electricity-tariffs/E-1R-NEW-26-10-13-C/standard-unit-rates/", octopustest.JSON(paged(rateRecord(noon, time.Time{}, 5))))
	env.mock.On("GET", "/products/NEW-26-10-13/electricity-tariffs/E-1R-NEW-26-10-13-C/standing-charges/", octopustest.JSON(paged(rateRecord(noon, time.Time{}, 40))))

	res := env.obj("octopus_compare_tariffs", map[string]any{"products": []string{"new-26-10-13", "FIX-12M-26-01-01", "G-1R-VAR-22-11-01-C", "not a code"}, "from": "2026-10-13", "to": "2026-10-13"})
	cur := res["current"].(Obj)
	if num(cur["unit_cost_gbp"]) != 9.6 || num(cur["standing_charge_gbp"]) != 0.5 || num(cur["total_cost_gbp"]) != 10.1 || asJSON(t, cur["tariff_codes"]) != `["E-1R-AGILE-24-10-01-C"]` {
		t.Errorf("current: %v", cur)
	}
	if num(get(res, "usage", "kwh")) != 48 || res["region"] != "C" || res["best"] != "E-1R-FIX-12M-26-01-01-C" {
		t.Errorf("result: %v", res)
	}
	rows := list(res["comparisons"])
	if len(rows) != 4 {
		t.Fatalf("comparisons: %v", rows)
	}
	fixed := rows[0].(Obj)
	if fixed["tariff_code"] != "E-1R-FIX-12M-26-01-01-C" || num(fixed["unit_cost_gbp"]) != 7.2 || num(fixed["total_cost_gbp"]) != 7.8 || num(fixed["difference_gbp"]) != -2.3 || num(fixed["avg_unit_rate_p_per_kwh"]) != 15 {
		t.Errorf("fixed: %v", fixed)
	}
	partial := rows[1].(Obj)
	if partial["tariff_code"] != "E-1R-NEW-26-10-13-C" || num(partial["half_hours_without_a_rate"]) != 24 || partial["difference_gbp"] != nil || partial["note"] == nil {
		t.Errorf("partial: %v", partial)
	}
	for _, r := range rows[2:] {
		if r.(Obj)["error"] == nil {
			t.Errorf("want an error: %v", r)
		}
	}
	if !strings.Contains(asJSON(t, rows), "gas tariff") {
		t.Errorf("fuel mismatch not explained: %v", rows)
	}
	// Product rates are public: the API key isn't sent.
	if env.mock.Last("/FIX-12M-26-01-01/").BasicUser != "" {
		t.Error("public rate request sent the API key")
	}

	for args, want := range map[string]map[string]any{
		"at least one product": {"products": []string{}},
		"at most 8":            {"products": []string{"A", "B", "C", "D", "E", "F", "G", "H", "I"}},
		"at most a year":       {"products": []string{"A"}, "from": "2025-01-01", "to": "2026-10-01"},
	} {
		if msg := env.callErr("octopus_compare_tariffs", want); !strings.Contains(msg, args) {
			t.Errorf("%v: %s", want, msg)
		}
	}
}

func TestCompareTariffsExport(t *testing.T) {
	env := newEnv(t)
	env.withAccount()
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, octopus.London)
	env.mock.On("GET", "/electricity-meter-points/1000000000002/meters/21E0000001/consumption/", octopustest.JSON(Obj{"count": 4, "next": nil, "results": halfHours(day.Add(12*time.Hour), 4, 2)}))
	env.mock.On("GET", "/products/OUTGOING-VAR-24-10-26/electricity-tariffs/E-1R-OUTGOING-VAR-24-10-26-C/standard-unit-rates/", octopustest.JSON(paged(rateRecord(day.AddDate(-1, 0, 0), time.Time{}, 15))))
	env.mock.On("GET", "/products/AGILE-OUTGOING-19-05-13/electricity-tariffs/E-1R-AGILE-OUTGOING-19-05-13-C/standard-unit-rates/", octopustest.JSON(paged(rateRecord(day.AddDate(-1, 0, 0), time.Time{}, 20))))
	res := env.obj("octopus_compare_tariffs", map[string]any{"direction": "export", "products": []string{"AGILE-OUTGOING-19-05-13"}, "from": "2026-10-13", "to": "2026-10-13"})
	// 8 kWh exported: £1.20 at 15p now, £1.60 at 20p.
	row := list(res["comparisons"])[0].(Obj)
	if num(get(res, "current", "unit_earnings_gbp")) != 1.2 || num(row["unit_earnings_gbp"]) != 1.6 || num(row["difference_gbp"]) != 0.4 || row["total_cost_gbp"] != nil || row["standing_charge_gbp"] != nil {
		t.Errorf("export: %v", res)
	}
}
