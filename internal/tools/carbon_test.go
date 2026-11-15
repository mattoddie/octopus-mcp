package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus/octopustest"
)

// carbonForecast is a Carbon Intensity API regional forecast with one
// half-hour per value from start, in the API's time format.
func carbonForecast(id int, name string, start time.Time, values ...float64) Obj {
	var data []any
	for i, v := range values {
		s := start.Add(time.Duration(i) * 30 * time.Minute).UTC()
		index := "moderate"
		if v < 120 {
			index = "low"
		}
		data = append(data, Obj{"from": s.Format(carbonTimeLayout), "to": s.Add(30 * time.Minute).Format(carbonTimeLayout), "intensity": Obj{"forecast": v, "index": index}, "generationmix": []any{}})
	}
	return Obj{"data": Obj{"regionid": id, "shortname": name, "data": data}}
}

func TestFindGreenerSlots(t *testing.T) {
	env := newEnv(t)
	agileDay(t, env, []float64{30, 25, 10, 12, 40, -2, 5, 30}, testNow)
	// The API also returns the half-hour before the one asked for.
	env.mock.On("GET", "/carbon/regional/intensity/[^/]+/fw48h/regionid/13", octopustest.JSON(carbonForecast(13, "London", testNow.Add(-30*time.Minute), 210, 200, 180, 100, 90, 150, 300, 250, 120, 80)))

	res := env.obj("octopus_find_greener_slots", map[string]any{"tariff_code": "E-1R-AGILE-24-10-01-C", "duration_hours": 1, "kwh": 2, "slots": 3, "to": "2026-10-15T17:00"})
	req := env.mock.Last("/carbon/")
	if req.Path != "/carbon/regional/intensity/2026-10-15T12:00Z/fw48h/regionid/13" || req.BasicUser != "" {
		t.Errorf("request: %+v", req)
	}
	best := res["greenest_block"].(Obj)
	if best["start"] != "2026-10-15T14:00:00+01:00" || best["end"] != "2026-10-15T15:00:00+01:00" || num(best["average_gco2_per_kwh"]) != 95 || num(best["average_p_per_kwh"]) != 11 || num(best["estimated_co2_kg"]) != 0.19 || num(best["estimated_cost_gbp"]) != 0.22 {
		t.Errorf("greenest block: %v", best)
	}
	alts := list(res["alternatives"])
	if len(alts) != 2 || get(alts[0], "start") != "2026-10-15T16:00:00+01:00" || get(alts[1], "start") != "2026-10-15T13:00:00+01:00" {
		t.Errorf("alternatives: %v", alts)
	}
	cheap := res["cheapest_block"].(Obj)
	if cheap["start"] != "2026-10-15T15:30:00+01:00" || num(cheap["average_p_per_kwh"]) != 1.5 || num(cheap["average_gco2_per_kwh"]) != 275 {
		t.Errorf("cheapest block: %v", cheap)
	}
	if num(get(res, "now", "gco2_per_kwh")) != 200 || get(res, "now", "index") != "moderate" || num(get(res, "region", "carbon_region_id")) != 13 || get(res, "region", "carbon_region_name") != "London" {
		t.Errorf("now and region: %v %v", res["now"], res["region"])
	}
	green := list(res["greenest_half_hours"])
	if len(green) != 3 || get(green[0], "start") != "2026-10-15T14:00:00+01:00" || num(get(green[1], "gco2_per_kwh")) != 90 || num(get(green[2], "p_per_kwh")) != 30 {
		t.Errorf("greenest half-hours: %v", green)
	}
	if res["forecast_until"] != "2026-10-15T17:00:00+01:00" || get(res, "tariff", "tariff_code") != "E-1R-AGILE-24-10-01-C" {
		t.Errorf("window: %v", res)
	}
}

func TestFindGreenerSlotsByRegionAlone(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/carbon/regional/intensity/[^/]+/fw48h/regionid/1", octopustest.JSON(carbonForecast(1, "North Scotland", testNow, 40, 20, 30, 10)))
	res := env.obj("octopus_find_greener_slots", map[string]any{"region": "P", "duration_hours": 0.5})
	if get(res, "greenest_block", "start") != "2026-10-15T14:30:00+01:00" || get(res, "greenest_block", "average_p_per_kwh") != nil || res["cheapest_block"] != nil || res["tariff"] != nil {
		t.Errorf("region only: %v", res)
	}
	if len(env.mock.Requests()) != 1 {
		t.Errorf("only the forecast should be fetched: %v", env.mock.Requests())
	}
	if msg := env.callErr("octopus_find_greener_slots", map[string]any{"region": "P", "duration_hours": 3}); !strings.Contains(msg, "fewer than") {
		t.Errorf("short forecast: %s", msg)
	}
	if msg := env.callErr("octopus_find_greener_slots", map[string]any{"region": "P", "from": "2026-10-20T12:00"}); !strings.Contains(msg, "48 hours") {
		t.Errorf("too far ahead: %s", msg)
	}
}
