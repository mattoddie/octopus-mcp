package tools

// octopus_compare_tariffs, in the consumption toolset: re-prices a meter's
// actual half-hourly readings on other tariffs.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
)

type compareArgs struct {
	MeterArgs
	Products       []string `json:"products" jsonschema:"Tariffs to compare, up to 8: product codes like AGILE-24-10-01 or GO-VAR-22-10-14 (from octopus_list_products; the region and fuel come from your meter), or full tariff codes like E-1R-AGILE-24-10-01-C"`
	From           string   `json:"from,omitempty" jsonschema:"Start of the usage to re-price: a date (YYYY-MM-DD, UK time) or date and time (default 30 days ago)"`
	To             string   `json:"to,omitempty" jsonschema:"End: a date (inclusive, the whole day) or date and time (default the end of yesterday)"`
	GasUnits       string   `json:"gas_units,omitempty" jsonschema:"Override the units a gas meter reports in: kwh or m3 (default: as Octopus says for the meter, otherwise m3)"`
	CalorificValue *float64 `json:"calorific_value,omitempty" jsonschema:"Gas calorific value in MJ/m³ for converting m³ to kWh (default 39.5; your bill shows the actual value)"`
}

const (
	maxCompareProducts = 8
	maxCompareDays     = 366
)

var productCodeRe = regexp.MustCompile(`^[A-Z0-9]+(-[A-Z0-9]+)*$`)

// candidateTariff turns a product or tariff code into a tariff for the meter's fuel and region.
func candidateTariff(code, fuel, region string) (octopus.Tariff, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if t, err := octopus.ParseTariff(code); err == nil {
		if t.Fuel != fuel {
			return t, fmt.Errorf("%s is a %s tariff, but the meter is %s", t.Code, t.Fuel, fuel)
		}
		return t, nil
	}
	if !productCodeRe.MatchString(code) {
		return octopus.Tariff{}, fmt.Errorf("%q is neither a product code (like AGILE-24-10-01) nor a tariff code (like E-1R-AGILE-24-10-01-C)", code)
	}
	return octopus.ParseTariff(octopus.TariffCode(fuel, "1R", code, region))
}

// priced totals readings at the rate a pricing function gives each half-hour.
type priced struct {
	kwh, pricedKWh, cost float64
	n, unknown           int
}

func priceReadings(readings []reading, perKWh float64, at func(time.Time) (float64, bool)) priced {
	var p priced
	for _, r := range readings {
		kwh := r.Value * perKWh
		p.kwh += kwh
		p.n++
		if rate, ok := at(r.Start); ok {
			p.cost += kwh * rate
			p.pricedKWh += kwh
		} else {
			p.unknown++
		}
	}
	return p
}

// tariffStandingCharges totals one tariff's daily standing charge for each UK
// day starting in [from, to), up to today.
func tariffStandingCharges(ctx context.Context, cl *octopus.Client, t octopus.Tariff, from, to time.Time) (float64, int, error) {
	rates, err := cl.TariffRates(ctx, t, "standing-charges", from.Add(-24*time.Hour), to)
	if err != nil {
		return 0, 0, err
	}
	total, days := 0.0, 0
	for d := startOfDay(from); d.Before(to) && !d.After(cl.Now()); d = time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, octopus.London) {
		if d.Before(from) {
			continue
		}
		if r, ok := rates.At(d); ok {
			total += r.IncVAT
			days++
		}
	}
	return total, days, nil
}

func registerCompare(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "consumption", Name: "octopus_compare_tariffs", Title: "Compare tariffs on your usage",
		Description: "Re-price your actual half-hourly smart meter readings on other Octopus tariffs, such as Agile, Go, Cosy, Tracker, Flux or a fixed deal, and compare them with what the tariffs you were on would have cost: unit costs at each tariff's real rate for every half-hour, plus standing charges, including VAT. " +
			"Give product codes from octopus_list_products (your meter's region and fuel are used) or full tariff codes. Works for electricity import, export (compares earnings) and gas. " +
			"These are estimates from past usage, which would change if you shifted it to suit a tariff; they ignore eligibility (Go and Intelligent Go need an EV or charger, Cosy a heat pump), exit fees, discounts and credits. Half-hours a tariff has no rate for (for example before a product existed) are left out and counted.",
		Enums: merge(meterEnums, map[string][]string{"gas_units": {"kwh", "m3"}}),
	}, func(ctx context.Context, a compareArgs) (any, error) {
		if len(a.Products) == 0 {
			return nil, errors.New("give at least one product code (see octopus_list_products) or tariff code in products")
		}
		if len(a.Products) > maxCompareProducts {
			return nil, fmt.Errorf("compare at most %d tariffs at a time", maxCompareProducts)
		}
		now := cl.Now()
		today := startOfDay(now)
		from, to, err := parseRange(a.From, a.To, today.AddDate(0, 0, -30), today, now)
		if err != nil {
			return nil, err
		}
		if to.Sub(from) > maxCompareDays*24*time.Hour {
			return nil, fmt.Errorf("compare at most a year of usage at a time")
		}
		number, _, mps, err := loadAccount(ctx, cl, a.AccountNumber)
		if err != nil {
			return nil, err
		}
		mp, err := pickMeterPoint(mps, a.MeterArgs)
		if err != nil {
			return nil, err
		}
		region := ""
		for i := len(mp.Agreements) - 1; i >= 0 && region == ""; i-- {
			region = mp.Agreements[i].Tariff.Region
		}
		if region == "" {
			return nil, fmt.Errorf("%s has no tariff to take the region from; pass full tariff codes", mp.label())
		}
		readings, serial, err := meterReadings(ctx, cl, mp, a.Serial, from, to)
		if err != nil {
			return nil, err
		}
		if len(readings) == 0 {
			return nil, errors.New("there are no smart meter readings in this period to compare with; readings usually arrive the next day, and meters that don't send half-hourly reads have none")
		}
		units, perKWh, assumptions := gasConversion(ctx, cl, number, mp, serial, a.GasUnits, a.CalorificValue)

		word := "cost"
		if mp.Export {
			word = "earnings"
		}
		summarize := func(p priced, standing float64, days int) Obj {
			o := Obj{"unit_" + word + "_gbp": pounds(p.cost)}
			if p.pricedKWh > 0 {
				o["avg_unit_rate_p_per_kwh"] = octopus.Round(p.cost/p.pricedKWh, 2)
			}
			if !mp.Export && days > 0 {
				o["standing_charge_gbp"] = pounds(standing)
				o["standing_charge_days"] = days
				o["total_cost_gbp"] = pounds(p.cost + standing)
			}
			if p.unknown > 0 {
				o["half_hours_without_a_rate"] = p.unknown
			}
			return o
		}
		total := func(p priced, standing float64) float64 {
			if mp.Export {
				return p.cost
			}
			return p.cost + standing
		}

		// What it cost on the tariffs actually in force.
		pr := newAgreementPricer(ctx, cl, mp, from, to)
		cur := priceReadings(readings, perKWh, pr.at)
		var curStanding float64
		var curDays int
		var errs []string
		if !mp.Export {
			if curStanding, curDays, err = standingCharges(ctx, cl, mp, from, to); err != nil {
				errs = append(errs, "standing charges: "+err.Error())
			}
		}
		errs = append(errs, pr.errs...)
		assumptions = append(assumptions, pr.assumptions...)
		current := summarize(cur, curStanding, curDays)
		var codes []string
		for _, ag := range mp.Agreements {
			if ag.From.Before(to) && (ag.To.IsZero() || ag.To.After(from)) {
				codes = append(codes, ag.Tariff.Code)
			}
		}
		current["tariff_codes"] = codes
		curTotal := total(cur, curStanding)

		// rank orders results: comparable ones, then partly priced ones, then errors.
		type result struct {
			row   Obj
			total float64
			rank  int
		}
		var results []result
		for _, code := range a.Products {
			t, err := candidateTariff(code, mp.Fuel, region)
			if err != nil {
				results = append(results, result{row: Obj{"requested": code, "error": err.Error()}, rank: 2})
				continue
			}
			row := Obj{"tariff_code": t.Code, "product_code": t.Product}
			l, err := cl.LoadUnitRates(ctx, t, from, to)
			if err != nil {
				row["error"] = err.Error()
				results = append(results, result{row: row, rank: 2})
				continue
			}
			if t.DayNight() {
				assumptions = append(assumptions, dayNightAssumption)
			}
			p := priceReadings(readings, perKWh, func(at time.Time) (float64, bool) {
				r, ok := l.At(at)
				return r.IncVAT, ok
			})
			if p.unknown == p.n {
				row["error"] = "this tariff has no unit rates for the period; check the product was on sale then and is sold in region " + region
				results = append(results, result{row: row, rank: 2})
				continue
			}
			var standing float64
			var days int
			if !mp.Export {
				if standing, days, err = tariffStandingCharges(ctx, cl, t, from, to); err != nil {
					row["standing_charge_error"] = err.Error()
				}
			}
			for k, v := range summarize(p, standing, days) {
				row[k] = v
			}
			tot := total(p, standing)
			complete := p.unknown == 0 && cur.unknown == 0 && (mp.Export || (days > 0 && days == curDays))
			if complete {
				diff := tot - curTotal
				row["difference_gbp"] = pounds(diff)
			} else {
				row["note"] = "Not every half-hour (or day's standing charge) could be priced on both this tariff and your own, so the totals aren't directly comparable."
			}
			rank := 1
			if complete {
				rank = 0
			}
			results = append(results, result{row: row, total: tot, rank: rank})
		}
		// Best first: lowest cost, or highest earnings for export. Errors last.
		sort.SliceStable(results, func(i, j int) bool {
			if results[i].rank != results[j].rank {
				return results[i].rank < results[j].rank
			}
			if mp.Export {
				return results[i].total > results[j].total
			}
			return results[i].total < results[j].total
		})
		rows := make([]Obj, 0, len(results))
		for _, r := range results {
			rows = append(rows, r.row)
		}

		out := Obj{
			"account_number": number,
			"meter":          Obj{"fuel": mp.Fuel, "meter_point": mp.ID, "direction": mp.direction(), "serial": serial},
			"region":         region,
			"from":           octopus.LocalTime(from),
			"to":             octopus.LocalTime(to),
			"usage":          Obj{"kwh": octopus.Round(cur.kwh, 2), "half_hours_with_data": cur.n},
			"current":        current,
			"comparisons":    rows,
		}
		if mp.Fuel == "gas" {
			delete(out["meter"].(Obj), "direction")
		}
		if units != "kWh" {
			out["usage"].(Obj)[units] = octopus.Round(cur.kwh/perKWh, 3)
		}
		if len(results) > 0 && results[0].rank == 0 {
			out["best"] = results[0].row["tariff_code"]
		}
		assumptions = append(assumptions, "Estimates from your past usage at each tariff's published rates, including VAT; your bills are the source of truth.")
		out["assumptions"] = unique(assumptions)
		if len(errs) > 0 {
			out["errors"] = errs
		}
		return out, nil
	})
}
