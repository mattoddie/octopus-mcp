package tools

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
	"github.com/mattoddie/octopus-mcp/internal/octopus/octopustest"
)

func TestListAndGetProducts(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/products/", func(r octopustest.Request, _ []string) (int, any) {
		if r.Query.Get("page") == "2" {
			return 200, Obj{"count": 3, "next": nil, "results": []any{Obj{"code": "VAR-22-11-01", "display_name": "Flexible Octopus", "direction": "IMPORT", "is_variable": true}}}
		}
		return 200, Obj{"count": 3, "next": "https://api.octopus.energy/v1/products/?brand=OCTOPUS_ENERGY&page=2", "results": []any{
			Obj{"code": "AGILE-24-10-01", "display_name": "Agile Octopus", "full_name": "Agile Octopus October 2024 v1", "direction": "IMPORT", "is_variable": true, "is_green": true, "term": 12},
			Obj{"code": "AGILE-OUTGOING-19-05-13", "display_name": "Agile Outgoing Octopus", "direction": "EXPORT", "is_variable": true},
		}}
	})
	res := env.obj("octopus_list_products", map[string]any{"search": "agile", "direction": "import"})
	items := list(res["items"])
	if len(items) != 1 || items[0].(Obj)["code"] != "AGILE-24-10-01" || items[0].(Obj)["green"] != true {
		t.Errorf("products: %v", res)
	}
	all := env.obj("octopus_list_products", nil)
	if num(all["total"]) != 3 {
		t.Errorf("pagination: %v", all)
	}
	// The next link is followed on the configured host, not the one in the link.
	if r := env.mock.Last("/products/"); r.Query.Get("page") != "2" || r.Query.Get("brand") != "OCTOPUS_ENERGY" {
		t.Errorf("next page: %+v", r)
	}
	if env.mock.Requests()[0].Query.Get("is_business") != "false" {
		t.Error("household products only by default")
	}

	env.mock.On("GET", "/industry/grid-supply-points/", octopustest.JSON(Obj{"count": 1, "results": []any{Obj{"group_id": "_C"}}}))
	env.mock.On("GET", "/products/OE-FIX-12M-26-10-10/", octopustest.JSON(jsonObj(t, `{
	  "code": "OE-FIX-12M-26-10-10", "display_name": "Octopus 12M Fixed", "is_variable": false, "term": 12, "description": "Fixed for a year.",
	  "single_register_electricity_tariffs": {
	    "_A": {"direct_debit_monthly": {"code": "E-1R-OE-FIX-12M-26-10-10-A", "standard_unit_rate_inc_vat": 24.1, "standing_charge_inc_vat": 50.1, "exit_fees_inc_vat": 0}},
	    "_C": {"direct_debit_monthly": {"code": "E-1R-OE-FIX-12M-26-10-10-C", "standard_unit_rate_inc_vat": 25.2, "standing_charge_inc_vat": 40.9555, "exit_fees_inc_vat": 5000}}
	  },
	  "dual_register_electricity_tariffs": {"_C": {"direct_debit_monthly": {"code": "E-2R-OE-FIX-12M-26-10-10-C", "day_unit_rate_inc_vat": 35.7465, "night_unit_rate_inc_vat": 15.1031, "standing_charge_inc_vat": 40.9555}}},
	  "single_register_gas_tariffs": {"_C": {"direct_debit_monthly": {"code": "G-1R-OE-FIX-12M-26-10-10-C", "standard_unit_rate_inc_vat": 9.231495, "standing_charge_inc_vat": 29.24082}}},
	  "sample_quotes": {"_C": {"direct_debit_monthly": {"electricity_single_rate": {"annual_cost_inc_vat": 87790}}}},
	  "sample_consumption": {"electricity_single_rate": {"electricity_standard": 2500}}
	}`)))
	p := env.obj("octopus_get_product", map[string]any{"product_code": "oe-fix-12m-26-10-10", "postcode": "SW1A 1AA"})
	if env.mock.Last("/grid-supply-points/").Query.Get("postcode") != "SW1A1AA" {
		t.Error("postcode lookup")
	}
	tariffs := list(p["tariffs"])
	if len(tariffs) != 3 {
		t.Fatalf("region filter: %v", tariffs)
	}
	single := tariffs[0].(Obj)
	if single["tariff_code"] != "E-1R-OE-FIX-12M-26-10-10-C" || num(single["unit_rate_p_per_kwh"]) != 25.2 || num(single["standing_charge_p_per_day"]) != 40.96 || num(single["exit_fee_gbp"]) != 50 {
		t.Errorf("single rate: %v", single)
	}
	if dual := tariffs[1].(Obj); num(dual["night_rate_p_per_kwh"]) != 15.1 {
		t.Errorf("dual rate: %v", dual)
	}
	if num(get(p, "sample_annual_costs", "electricity_single_rate_gbp_per_year")) != 877.9 {
		t.Errorf("sample quote: %v", p["sample_annual_costs"])
	}
}

func agileDay(t *testing.T, env *testEnv, prices []float64, start time.Time) {
	t.Helper()
	var records []Obj
	for i, p := range prices {
		s := start.Add(time.Duration(i) * 30 * time.Minute)
		records = append(records, rateRecord(s, s.Add(30*time.Minute), p))
	}
	env.mock.On("GET", "/products/AGILE-24-10-01/electricity-tariffs/E-1R-AGILE-24-10-01-C/standard-unit-rates/", octopustest.JSON(paged(records...)))
}

func TestUnitRatesForTheAccountsTariff(t *testing.T) {
	env := newEnv(t)
	env.withAccount()
	// From 12:00 UTC (13:00 UK, the test clock) in half-hours.
	agileDay(t, env, []float64{30, 25, 10, 12, 40, -2, 5, 30}, testNow)
	res := env.obj("octopus_get_unit_rates", nil)
	if get(res, "tariff", "source") != "account" || get(res, "tariff", "tariff_code") != "E-1R-AGILE-24-10-01-C" {
		t.Errorf("tariff: %v", res["tariff"])
	}
	s := res["summary"].(Obj)
	if num(get(s, "now", "p_per_kwh")) != 30 || num(get(s, "next_change", "p_per_kwh")) != 25 || num(s["min_p_per_kwh"]) != -2 || num(s["negative_price_periods"]) != 1 {
		t.Errorf("summary: %v", s)
	}
	if len(list(res["rates"])) != 8 || get(list(res["rates"])[0], "from") != "2026-10-15T13:00:00+01:00" {
		t.Errorf("rates should be oldest first in UK time: %v", res["rates"])
	}
	q := env.mock.Last("/standard-unit-rates/").Query
	if q.Get("period_from") != "2026-10-14T23:00:00Z" || q.Get("period_to") != "2026-10-16T23:00:00Z" {
		t.Errorf("default period should be today and tomorrow: %v", q)
	}

	byProduct := env.obj("octopus_get_unit_rates", map[string]any{"product_code": "AGILE-24-10-01", "region": "_c", "from": "now", "to": "2026-10-15T15:00"})
	if get(byProduct, "tariff", "tariff_code") != "E-1R-AGILE-24-10-01-C" || len(list(byProduct["rates"])) != 4 {
		t.Errorf("by product: %v", byProduct)
	}
	if msg := env.callErr("octopus_get_unit_rates", map[string]any{"product_code": "AGILE-24-10-01"}); !strings.Contains(msg, "region") {
		t.Errorf("missing region: %s", msg)
	}
	if msg := env.callErr("octopus_get_unit_rates", map[string]any{"tariff_code": "AGILE"}); !strings.Contains(msg, "not a tariff code") {
		t.Errorf("bad tariff code: %s", msg)
	}
}

func TestDayNightUnitRates(t *testing.T) {
	env := newEnv(t)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	env.mock.On("GET", "/products/VAR-22-11-01/electricity-tariffs/E-2R-VAR-22-11-01-B/day-unit-rates/", octopustest.JSON(paged(rateRecord(from, time.Time{}, 30))))
	env.mock.On("GET", "/products/VAR-22-11-01/electricity-tariffs/E-2R-VAR-22-11-01-B/night-unit-rates/", octopustest.JSON(paged(rateRecord(from, time.Time{}, 14))))
	res := env.obj("octopus_get_unit_rates", map[string]any{"tariff_code": "E-2R-VAR-22-11-01-B"})
	if num(get(res, "day", "summary", "now", "p_per_kwh")) != 30 || num(get(res, "night", "summary", "now", "p_per_kwh")) != 14 || !strings.Contains(fmt.Sprint(res["note"]), "Economy 7") {
		t.Errorf("day/night: %v", res)
	}
}

func TestStandingCharges(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/products/AGILE-24-10-01/electricity-tariffs/E-1R-AGILE-24-10-01-C/standing-charges/", octopustest.JSON(paged(
		rateRecord(time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), 45),
		rateRecord(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), time.Time{}, 50),
	)))
	res := env.obj("octopus_get_standing_charges", map[string]any{"tariff_code": "E-1R-AGILE-24-10-01-C"})
	if num(res["current_p_per_day"]) != 50 || num(res["current_gbp_per_year"]) != 182.5 || len(list(res["charges"])) != 2 {
		t.Errorf("standing charges: %v", res)
	}
}

func TestFindCheapestSlots(t *testing.T) {
	env := newEnv(t)
	agileDay(t, env, []float64{30, 25, 10, 12, 40, -2, 5, 30}, testNow)
	res := env.obj("octopus_find_cheapest_slots", map[string]any{"tariff_code": "E-1R-AGILE-24-10-01-C", "duration_hours": 1, "slots": 3, "kwh": 2})
	best := res["best_block"].(Obj)
	if best["start"] != "2026-10-15T15:30:00+01:00" || best["end"] != "2026-10-15T16:30:00+01:00" || num(best["average_p_per_kwh"]) != 1.5 || num(best["estimated_cost_gbp"]) != 0.03 {
		t.Errorf("best block: %v", best)
	}
	alts := list(res["alternatives"])
	if len(alts) != 2 || get(alts[0], "start") != "2026-10-15T14:00:00+01:00" {
		t.Errorf("alternatives should not overlap the best block: %v", alts)
	}
	slots := list(res["cheapest_half_hours"])
	if len(slots) != 3 || num(get(slots[0], "p_per_kwh")) != 10 || num(get(slots[1], "p_per_kwh")) != -2 {
		t.Errorf("cheapest half-hours (in time order): %v", slots)
	}
	if num(res["cost_if_run_now_gbp"]) != 0.6 || res["prices_known_until"] != "2026-10-15T17:00:00+01:00" {
		t.Errorf("comparison: %v", res)
	}
	if msg := env.callErr("octopus_find_cheapest_slots", map[string]any{"tariff_code": "E-1R-AGILE-24-10-01-C", "duration_hours": 5}); !strings.Contains(msg, "fewer than") {
		t.Errorf("too long: %s", msg)
	}
	if msg := env.callErr("octopus_find_cheapest_slots", map[string]any{"tariff_code": "E-1R-AGILE-24-10-01-C", "duration_hours": 0.75}); !strings.Contains(msg, "multiple of 0.5") {
		t.Errorf("odd duration: %s", msg)
	}

	// A flat tariff: timing doesn't matter.
	env.mock.On("GET", "/products/VAR-22-11-01/electricity-tariffs/E-1R-VAR-22-11-01-C/standard-unit-rates/", octopustest.JSON(paged(rateRecord(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Time{}, 24.5))))
	flat := env.obj("octopus_find_cheapest_slots", map[string]any{"tariff_code": "E-1R-VAR-22-11-01-C", "to": "2026-10-15T20:00"})
	if !strings.Contains(fmt.Sprint(flat["note"]), "same throughout") {
		t.Errorf("flat tariff: %v", flat)
	}
}

func TestFindRegion(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/industry/grid-supply-points/", func(r octopustest.Request, _ []string) (int, any) {
		if r.Query.Get("postcode") == "ZZ11ZZ" {
			return 200, Obj{"count": 0, "results": []any{}}
		}
		return 200, Obj{"count": 1, "results": []any{Obj{"group_id": "_P"}}}
	})
	res := env.obj("octopus_find_region", map[string]any{"postcode": "ab10 1aa"})
	if res["region"] != "P" || res["region_name"] != "Northern Scotland" {
		t.Errorf("region: %v", res)
	}
	if msg := env.callErr("octopus_find_region", map[string]any{"postcode": "ZZ1 1ZZ"}); !strings.Contains(msg, "doesn't know") {
		t.Errorf("unknown postcode: %s", msg)
	}
}

func TestTariffParsing(t *testing.T) {
	tr, err := octopus.ParseTariff("e-1r-agile-24-10-01-c")
	if err != nil || tr.Product != "AGILE-24-10-01" || tr.Region != "C" || tr.Fuel != "electricity" || tr.DayNight() {
		t.Errorf("parse: %+v %v", tr, err)
	}
	if code := octopus.TariffCode("gas", "", "VAR-22-11-01", "_m"); code != "G-1R-VAR-22-11-01-M" {
		t.Errorf("build: %s", code)
	}
	if _, err := octopus.ParseTariff("E-1R-AGILE-24-10-01-Z"); err == nil {
		t.Error("region Z accepted")
	}
}
