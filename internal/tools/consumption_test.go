package tools

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
	"github.com/mattoddie/octopus-mcp/internal/octopus/octopustest"
)

// halfHours returns n consecutive consumption records from start, each with value v.
func halfHours(start time.Time, n int, v float64) []any {
	var out []any
	for i := 0; i < n; i++ {
		s := start.Add(time.Duration(i) * 30 * time.Minute)
		out = append(out, Obj{"consumption": v, "interval_start": s.Format(time.RFC3339), "interval_end": s.Add(30 * time.Minute).Format(time.RFC3339)})
	}
	return out
}

func TestConsumptionWithAgileCost(t *testing.T) {
	env := newEnv(t)
	env.withAccount()
	// 13 October 2026 in UK time (BST) starts at 23:00 UTC the day before.
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, octopus.London)
	noon := day.Add(12 * time.Hour)
	env.mock.On("GET", "/electricity-meter-points/1000000000001/meters/21E0000001/consumption/", func(r octopustest.Request, _ []string) (int, any) {
		return 200, Obj{"count": 48, "next": nil, "results": halfHours(day, 48, 0.5)}
	})
	env.mock.On("GET", "/products/AGILE-24-10-01/electricity-tariffs/E-1R-AGILE-24-10-01-C/standard-unit-rates/", octopustest.JSON(paged(
		rateRecord(day.Add(-time.Hour), noon, 10), rateRecord(noon, day.Add(25*time.Hour), 20),
	)))
	env.mock.On("GET", "/products/AGILE-24-10-01/electricity-tariffs/E-1R-AGILE-24-10-01-C/standing-charges/", octopustest.JSON(paged(
		rateRecord(time.Date(2024, 9, 30, 23, 0, 0, 0, time.UTC), time.Time{}, 50),
	)))

	res := env.obj("octopus_get_consumption", map[string]any{"from": "2026-10-13", "to": "2026-10-13"})
	q := env.mock.Last("/consumption/").Query
	if q.Get("period_from") != "2026-10-12T23:00:00Z" || q.Get("period_to") != "2026-10-13T23:00:00Z" || q.Get("order_by") != "period" || q.Get("page_size") != "25000" {
		t.Errorf("consumption query: %v", q)
	}
	if env.mock.Last("/consumption/").BasicUser != octopustest.APIKey {
		t.Error("consumption needs the API key")
	}
	if env.mock.Last("/standard-unit-rates/").BasicUser != "" {
		t.Error("public rate requests shouldn't send the API key")
	}
	s := res["summary"].(Obj)
	// 24 kWh: 12 at 10p and 12 at 20p = £3.60, plus a 50p standing charge.
	if num(s["kWh"]) != 24 || num(s["unit_cost_gbp"]) != 3.6 || num(s["standing_charge_gbp"]) != 0.5 || num(s["total_cost_gbp"]) != 4.1 || num(s["avg_unit_rate_p_per_kwh"]) != 15 {
		t.Errorf("summary: %v", s)
	}
	if num(s["half_hours_with_data"]) != 48 || num(s["half_hours_in_period"]) != 48 {
		t.Errorf("completeness: %v", s)
	}
	rows := list(res["rows"])
	if len(rows) != 1 || rows[0].(Obj)["start"] != "2026-10-13" || num(rows[0].(Obj)["cost_gbp"]) != 3.6 {
		t.Errorf("rows: %v", rows)
	}
	if tariffs := list(res["tariffs"]); len(tariffs) != 1 || tariffs[0].(Obj)["product_code"] != "AGILE-24-10-01" {
		t.Errorf("tariffs in period: %v", res["tariffs"])
	}

	hourly := env.obj("octopus_get_consumption", map[string]any{"from": "2026-10-13", "to": "2026-10-13", "group_by": "hour", "include_cost": false})
	if rows := list(hourly["rows"]); len(rows) != 24 || num(rows[0].(Obj)["kWh"]) != 1 || rows[0].(Obj)["start"] != "2026-10-13T00:00:00+01:00" || rows[0].(Obj)["cost_gbp"] != nil {
		t.Errorf("hourly: %v", hourly["rows"])
	}
	capped := env.obj("octopus_get_consumption", map[string]any{"from": "2026-10-13", "to": "2026-10-13", "group_by": "half_hour", "max_rows": 10})
	if len(list(capped["rows"])) != 10 || capped["truncated"] != true {
		t.Errorf("max_rows: %v", capped)
	}
}

func TestConsumptionGasInCubicMetres(t *testing.T) {
	env := newEnv(t)
	env.withAccount()
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, octopus.London)
	env.mock.On("GET", "/gas-meter-points/3000000001/meters/G4A0000001/consumption/", octopustest.JSON(Obj{"count": 2, "results": halfHours(day, 2, 1)}))
	env.mock.On("GET", "/products/VAR-22-11-01/gas-tariffs/G-1R-VAR-22-11-01-C/standard-unit-rates/", octopustest.JSON(paged(rateRecord(day.AddDate(0, -1, 0), time.Time{}, 6))))
	env.mock.On("GET", "/products/VAR-22-11-01/gas-tariffs/G-1R-VAR-22-11-01-C/standing-charges/", octopustest.JSON(paged(rateRecord(day.AddDate(0, -1, 0), time.Time{}, 30))))

	res := env.obj("octopus_get_consumption", map[string]any{"fuel": "gas", "from": "2026-10-13", "to": "2026-10-13"})
	s := res["summary"].(Obj)
	kwh := 2 * 1.02264 * 39.5 / 3.6
	if res["units"] != "m³" || num(s["m³"]) != 2 || math.Abs(num(s["kwh"])-kwh) > 0.01 {
		t.Errorf("gas units: %v", res)
	}
	if want := math.Round(kwh*6) / 100; math.Abs(num(s["unit_cost_gbp"])-want) > 0.011 {
		t.Errorf("gas cost: %v, want %v", s["unit_cost_gbp"], want)
	}
	if !strings.Contains(fmt.Sprint(res["assumptions"]), "39.5") {
		t.Errorf("assumptions: %v", res["assumptions"])
	}
	// The user can say the meter reports kWh (a SMETS1 meter).
	res = env.obj("octopus_get_consumption", map[string]any{"fuel": "gas", "from": "2026-10-13", "to": "2026-10-13", "gas_units": "kwh"})
	if res["units"] != "kWh" || num(res["summary"].(Obj)["unit_cost_gbp"]) != 0.12 {
		t.Errorf("gas kWh override: %v", res)
	}
}

func TestConsumptionExportAndMeterSelection(t *testing.T) {
	env := newEnv(t)
	env.withAccount()
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, octopus.London)
	env.mock.On("GET", "/electricity-meter-points/1000000000002/meters/21E0000001/consumption/", octopustest.JSON(Obj{"count": 2, "results": halfHours(day.Add(12*time.Hour), 2, 1.5)}))
	env.mock.On("GET", "/products/OUTGOING-VAR-24-10-26/electricity-tariffs/E-1R-OUTGOING-VAR-24-10-26-C/standard-unit-rates/", octopustest.JSON(paged(rateRecord(day.AddDate(-1, 0, 0), time.Time{}, 15))))
	res := env.obj("octopus_get_consumption", map[string]any{"direction": "export", "from": "2026-10-13", "to": "2026-10-13"})
	s := res["summary"].(Obj)
	if num(s["unit_earnings_gbp"]) != 0.45 || s["standing_charge_gbp"] != nil || get(res, "meter", "direction") != "export" {
		t.Errorf("export: %v", res)
	}
	if msg := env.callErr("octopus_get_consumption", map[string]any{"meter_point": "999"}); !strings.Contains(msg, "isn't on this account") {
		t.Errorf("unknown meter point: %s", msg)
	}
	if msg := env.callErr("octopus_get_consumption", map[string]any{"from": "2026-10-13", "to": "2026-10-01"}); !strings.Contains(msg, "must be after") {
		t.Errorf("bad range: %s", msg)
	}
}

func TestConsumptionNoData(t *testing.T) {
	env := newEnv(t)
	env.withAccount()
	env.mock.On("GET", "/electricity-meter-points/.*/consumption/", octopustest.JSON(Obj{"count": 0, "results": []any{}}))
	res := env.obj("octopus_get_consumption", map[string]any{"include_cost": false})
	if !strings.Contains(fmt.Sprint(res["note"]), "No readings") || res["from"] != "2026-10-08T00:00:00+01:00" || res["to"] != "2026-10-15T00:00:00+01:00" {
		t.Errorf("no data: %v", res)
	}
}

func TestLiveUsage(t *testing.T) {
	env := newEnv(t)
	env.withAccount()
	env.mock.OnGraphQL(`smartMeterTelemetry`, octopustest.Data(Obj{"smartMeterTelemetry": []any{
		Obj{"readAt": "2026-10-15T11:59:00+00:00", "demand": "450", "consumptionDelta": "8.0", "consumption": "1234567"},
		Obj{"readAt": "2026-10-15T11:58:00+00:00", "demand": "350", "consumptionDelta": "6.0", "consumption": "1234559"},
	}}))
	res := env.obj("octopus_get_live_usage", nil)
	latest := res["latest"].(Obj)
	if num(latest["demand_w"]) != 450 || latest["read_at"] != "2026-10-15T12:59:00+01:00" || num(latest["meter_import_total_kwh"]) != 1234.567 {
		t.Errorf("latest: %v", latest)
	}
	if num(res["average_demand_w"]) != 400 || num(res["kwh_in_window"]) != 0.014 || res["device_id"] != "00-11-22-33-44-55-66-77" {
		t.Errorf("live: %v", res)
	}
	vars := env.mock.LastGraphQL("smartMeterTelemetry").Variables
	if vars["grouping"] != "ONE_MINUTE" || vars["start"] != "2026-10-15T11:00:00Z" || vars["device"] != "00-11-22-33-44-55-66-77" {
		t.Errorf("telemetry variables: %v", vars)
	}
	env.mock.OnGraphQL(`smartMeterTelemetry`, octopustest.Data(Obj{"smartMeterTelemetry": []any{}}))
	if res := env.obj("octopus_get_live_usage", map[string]any{"device_id": "x", "minutes": 120}); !strings.Contains(fmt.Sprint(res["note"]), "Home Mini") || res["grouping"] != "FIVE_MINUTES" {
		t.Errorf("no live data: %v", res)
	}
}

func TestParseWhen(t *testing.T) {
	for in, want := range map[string]string{
		"2026-10-13":                "2026-10-13T00:00:00+01:00",
		"today":                     "2026-10-15T00:00:00+01:00",
		"yesterday":                 "2026-10-14T00:00:00+01:00",
		"2026-10-13T17:30":          "2026-10-13T17:30:00+01:00",
		"2026-12-01 08:00":          "2026-12-01T08:00:00Z",
		"2026-10-13T16:00:00Z":      "2026-10-13T17:00:00+01:00",
		"now":                       "2026-10-15T13:00:00+01:00",
		"2026-03-29T12:00:00+02:00": "2026-03-29T11:00:00+01:00",
	} {
		got, err := parseWhen(in, false, testNow)
		if err != nil || octopus.LocalTime(got) != want {
			t.Errorf("%s: got %v %v, want %s", in, octopus.LocalTime(got), err, want)
		}
	}
	end, _ := parseWhen("2026-10-13", true, testNow)
	if octopus.LocalTime(end) != "2026-10-14T00:00:00+01:00" {
		t.Errorf("end of day: %v", end)
	}
	// The day the clocks change is 23 hours long.
	end, _ = parseWhen("2026-03-29", true, testNow)
	start, _ := parseWhen("2026-03-29", false, testNow)
	if end.Sub(start) != 23*time.Hour {
		t.Errorf("spring forward day: %v", end.Sub(start))
	}
	if _, err := parseWhen("next tuesday", false, testNow); err == nil {
		t.Error("nonsense accepted")
	}
}
