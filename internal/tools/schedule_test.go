package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
	"github.com/mattoddie/octopus-mcp/internal/octopus/octopustest"
)

func TestPlanSchedule(t *testing.T) {
	env := newEnv(t)
	// From 13:00 UK time: 30, 25, 10, 12, 40, -2, 5, 30p.
	agileDay(t, env, []float64{30, 25, 10, 12, 40, -2, 5, 30}, testNow)
	// 5 kWh at 3 kW is 1.5 kWh a half-hour: three full half-hours at -2p, 5p and 10p,
	// and the last 0.5 kWh at 12p.
	res := env.obj("octopus_plan_schedule", map[string]any{"tariff_code": "E-1R-AGILE-24-10-01-C", "energy_kwh": 5, "power_kw": 3})
	sched := list(res["schedule"])
	if len(sched) != 2 {
		t.Fatalf("schedule: %v", sched)
	}
	first, second := sched[0].(Obj), sched[1].(Obj)
	if first["start"] != "2026-10-15T14:00:00+01:00" || first["end"] != "2026-10-15T15:00:00+01:00" || num(first["kwh"]) != 2 || first["note"] == nil {
		t.Errorf("first window: %v", first)
	}
	if second["start"] != "2026-10-15T15:30:00+01:00" || second["end"] != "2026-10-15T16:30:00+01:00" || num(second["kwh"]) != 3 || num(second["average_p_per_kwh"]) != 1.5 {
		t.Errorf("second window: %v", second)
	}
	// 1.5×10 + 0.5×12 + 1.5×(-2) + 1.5×5 = 25.5p. Starting now: 1.5×30 + 1.5×25 + 1.5×10 + 0.5×12 = 103.5p.
	if num(res["total_cost_gbp"]) != 0.26 || num(res["cost_if_started_now_gbp"]) != 1.03 || num(res["saving_gbp"]) != 0.78 || num(res["hours_running"]) != 2 || num(res["average_p_per_kwh"]) != 5.1 {
		t.Errorf("totals: %v", res)
	}
	if bands := list(res["tariff_windows"]); len(bands) != 8 || num(get(bands[5], "p_per_kwh")) != -2 {
		t.Errorf("tariff windows: %v", bands)
	}

	for want, args := range map[string]map[string]any{
		"energy_kwh must be":  {"tariff_code": "E-1R-AGILE-24-10-01-C", "energy_kwh": 0, "power_kw": 3},
		"power_kw must be":    {"tariff_code": "E-1R-AGILE-24-10-01-C", "energy_kwh": 5, "power_kw": 0},
		"at most 12.0 kWh":    {"tariff_code": "E-1R-AGILE-24-10-01-C", "energy_kwh": 20, "power_kw": 3},
		"day/night":           {"tariff_code": "E-2R-VAR-22-11-01-C", "energy_kwh": 5, "power_kw": 3},
		"plan at most a week": {"tariff_code": "E-1R-AGILE-24-10-01-C", "energy_kwh": 5, "power_kw": 3, "to": "2026-10-30"},
	} {
		if msg := env.callErr("octopus_plan_schedule", args); !strings.Contains(msg, want) {
			t.Errorf("%v: %s", args, msg)
		}
	}
}

// Cosy-style bands: the plan uses every cheap window, not one block.
func TestPlanScheduleAcrossWindows(t *testing.T) {
	env := newEnv(t)
	day := time.Date(2026, 10, 16, 0, 0, 0, 0, octopus.London)
	h := func(x float64) time.Time { return day.Add(time.Duration(x * float64(time.Hour))) }
	env.mock.On("GET", "/products/COSY-22-12-08/electricity-tariffs/E-1R-COSY-22-12-08-C/standard-unit-rates/", octopustest.JSON(paged(
		rateRecord(h(0), h(4), 25), rateRecord(h(4), h(7), 12), rateRecord(h(7), h(13), 25),
		rateRecord(h(13), h(16), 12), rateRecord(h(16), h(19), 40), rateRecord(h(19), h(22), 25),
		rateRecord(h(22), h(24), 12),
	)))
	res := env.obj("octopus_plan_schedule", map[string]any{"tariff_code": "E-1R-COSY-22-12-08-C", "energy_kwh": 16, "power_kw": 2, "from": "2026-10-16", "to": "2026-10-16"})
	sched := list(res["schedule"])
	// 16 kWh at 2 kW is 8 hours, all in the 12p windows: 04:00-07:00, 13:00-16:00, then 2 hours from 22:00.
	if len(sched) != 3 || get(sched[0], "start") != "2026-10-16T04:00:00+01:00" || get(sched[1], "end") != "2026-10-16T16:00:00+01:00" || get(sched[2], "end") != "2026-10-17T00:00:00+01:00" {
		t.Errorf("schedule: %v", sched)
	}
	if num(res["average_p_per_kwh"]) != 12 || len(list(res["tariff_windows"])) != 7 {
		t.Errorf("result: %v", res)
	}
}
