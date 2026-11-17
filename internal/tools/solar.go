package tools

// octopus_get_solar_summary, in the consumption toolset: puts the import and
// export meters side by side for households with solar panels or a battery.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
)

type solarArgs struct {
	AccountNumber    string   `json:"account_number,omitempty" jsonschema:"Account number like A-1234ABCD (default OCTOPUS_ACCOUNT, or the only account the API key can see)"`
	From             string   `json:"from,omitempty" jsonschema:"Start: a date (YYYY-MM-DD, UK time) or date and time (default 30 days ago)"`
	To               string   `json:"to,omitempty" jsonschema:"End: a date (inclusive, the whole day) or date and time (default the end of yesterday)"`
	GroupBy          string   `json:"group_by,omitempty" jsonschema:"How to total the half-hours: day (default), week (Monday to Sunday), month or total"`
	ImportMeterPoint string   `json:"import_meter_point,omitempty" jsonschema:"The import MPAN, when the account has several (see octopus_get_account)"`
	ExportMeterPoint string   `json:"export_meter_point,omitempty" jsonschema:"The export MPAN, when the account has several (see octopus_get_account)"`
	GeneratedKWh     *float64 `json:"generated_kwh,omitempty" jsonschema:"What the panels generated in the whole period, in kWh, from your inverter or generation meter. Octopus doesn't have this, so self-consumption is only worked out when you give it"`
	Limit            *int     `json:"limit,omitempty" jsonschema:"Return at most this many rows, the most recent (default 100)"`
}

func registerSolar(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "consumption", Name: "octopus_get_solar_summary", Title: "Solar and export summary",
		Description: "Combine a home's electricity import and export smart meters (for solar panels or a battery) over a period: kWh imported and exported, what the imports cost and the exports earned at the actual rate for every half-hour (so Agile Outgoing and other time-of-use export tariffs are priced correctly), standing charges and the net cost, by day, week or month. " +
			"Octopus has no generation data, so to get self-consumption (generation minus export) and self-sufficiency, pass generated_kwh from your inverter app. Readings are usually a day behind. Estimates exclude discounts and credits; your bill is the source of truth.",
		Enums: map[string][]string{"group_by": {"day", "week", "month", "total"}},
	}, func(ctx context.Context, a solarArgs) (any, error) {
		now := cl.Now()
		today := startOfDay(now)
		from, to, err := parseRange(a.From, a.To, today.AddDate(0, 0, -30), today, now)
		if err != nil {
			return nil, err
		}
		if to.Sub(from) > maxCompareDays*24*time.Hour {
			return nil, fmt.Errorf("ask for at most a year at a time")
		}
		groupBy := strings.ToLower(a.GroupBy)
		if groupBy == "" {
			groupBy = "day"
		}
		if a.GeneratedKWh != nil && *a.GeneratedKWh < 0 {
			return nil, fmt.Errorf("generated_kwh can't be negative")
		}
		number, _, mps, err := loadAccount(ctx, cl, a.AccountNumber)
		if err != nil {
			return nil, err
		}
		imp, err := pickMeterPoint(mps, MeterArgs{Direction: "import", MeterPoint: a.ImportMeterPoint})
		if err != nil {
			return nil, err
		}
		exp, err := pickMeterPoint(mps, MeterArgs{Direction: "export", MeterPoint: a.ExportMeterPoint})
		if err != nil {
			return nil, fmt.Errorf("%w; this summary needs an export meter, which Octopus sets up when you have an export tariff such as Outgoing Octopus", err)
		}
		if imp.Export || imp.Fuel != "electricity" {
			return nil, fmt.Errorf("import_meter_point %s isn't an electricity import meter point", imp.ID)
		}
		if !exp.Export {
			return nil, fmt.Errorf("export_meter_point %s isn't an export meter point", exp.ID)
		}
		impReadings, impSerial, err := meterReadings(ctx, cl, imp, "", from, to)
		if err != nil {
			return nil, err
		}
		expReadings, expSerial, err := meterReadings(ctx, cl, exp, "", from, to)
		if err != nil {
			return nil, err
		}

		type group struct {
			start                              time.Time
			impKWh, expKWh, impCost, expEarned float64
		}
		var groups []*group
		index := map[time.Time]*group{}
		groupFor := func(t time.Time) *group {
			k := groupStart(t, groupBy)
			g, ok := index[k]
			if !ok {
				g = &group{start: k}
				index[k] = g
				groups = append(groups, g)
			}
			return g
		}
		total := &group{}
		impPricer := newAgreementPricer(ctx, cl, imp, from, to)
		expPricer := newAgreementPricer(ctx, cl, exp, from, to)
		var impUnpriced, expUnpriced, exportingHalfHours int
		var impPricedKWh, expPricedKWh float64
		for _, r := range impReadings {
			g := groupFor(r.Start)
			g.impKWh += r.Value
			total.impKWh += r.Value
			if p, ok := impPricer.at(r.Start); ok {
				g.impCost += r.Value * p
				total.impCost += r.Value * p
				impPricedKWh += r.Value
			} else {
				impUnpriced++
			}
		}
		for _, r := range expReadings {
			g := groupFor(r.Start)
			g.expKWh += r.Value
			total.expKWh += r.Value
			if r.Value > 0 {
				exportingHalfHours++
			}
			if p, ok := expPricer.at(r.Start); ok {
				g.expEarned += r.Value * p
				total.expEarned += r.Value * p
				expPricedKWh += r.Value
			} else {
				expUnpriced++
			}
		}
		// Imports and exports are fetched separately, so put the groups back in order.
		sort.Slice(groups, func(i, j int) bool { return groups[i].start.Before(groups[j].start) })

		var errs []string
		standing, days, err := standingCharges(ctx, cl, imp, from, to)
		if err != nil {
			errs = append(errs, "standing charges: "+err.Error())
		}
		errs = append(errs, impPricer.errs...)
		errs = append(errs, expPricer.errs...)

		var rows []Obj
		if groupBy != "total" {
			for _, g := range groups {
				rows = append(rows, Obj{
					"start":               groupLabel(g.start, groupBy),
					"import_kwh":          octopus.Round(g.impKWh, 2),
					"export_kwh":          octopus.Round(g.expKWh, 2),
					"import_cost_gbp":     pounds(g.impCost),
					"export_earnings_gbp": pounds(g.expEarned),
					"net_unit_cost_gbp":   pounds(g.impCost - g.expEarned),
				})
			}
		}
		maxRows := limitArg(a.Limit, 100, 2000)
		truncated := false
		if len(rows) > maxRows {
			rows = rows[len(rows)-maxRows:]
			truncated = true
		}

		summary := Obj{
			"import_kwh":                  octopus.Round(total.impKWh, 2),
			"export_kwh":                  octopus.Round(total.expKWh, 2),
			"net_import_kwh":              octopus.Round(total.impKWh-total.expKWh, 2),
			"import_unit_cost_gbp":        pounds(total.impCost),
			"export_earnings_gbp":         pounds(total.expEarned),
			"half_hours_exporting":        exportingHalfHours,
			"import_half_hours_with_data": len(impReadings),
			"export_half_hours_with_data": len(expReadings),
		}
		if impPricedKWh > 0 {
			summary["avg_import_rate_p_per_kwh"] = octopus.Round(total.impCost/impPricedKWh, 2)
		}
		if expPricedKWh > 0 {
			summary["avg_export_rate_p_per_kwh"] = octopus.Round(total.expEarned/expPricedKWh, 2)
		}
		if days > 0 {
			summary["standing_charge_gbp"] = pounds(standing)
			summary["standing_charge_days"] = days
			summary["net_cost_gbp"] = pounds(total.impCost + standing - total.expEarned)
		}
		if impUnpriced > 0 {
			summary["import_half_hours_without_a_rate"] = impUnpriced
		}
		if expUnpriced > 0 {
			summary["export_half_hours_without_a_rate"] = expUnpriced
		}

		var notes []string
		if a.GeneratedKWh != nil {
			gen := *a.GeneratedKWh
			self := gen - total.expKWh
			if self < 0 {
				notes = append(notes, fmt.Sprintf("generated_kwh (%.1f) is less than what was exported (%.1f), so self-consumption couldn't be worked out; check the generation figure covers the same period. A battery charged from the grid can also export more than the panels made.", gen, total.expKWh))
			} else {
				sol := Obj{
					"generated_kwh":     octopus.Round(gen, 2),
					"self_consumed_kwh": octopus.Round(self, 2),
					"home_use_kwh":      octopus.Round(total.impKWh+self, 2),
				}
				if gen > 0 {
					sol["self_consumption_percent"] = octopus.Round(100*self/gen, 1)
				}
				if home := total.impKWh + self; home > 0 {
					sol["self_sufficiency_percent"] = octopus.Round(100*self/home, 1)
				}
				if impPricedKWh > 0 {
					sol["self_consumption_saving_gbp"] = pounds(self * total.impCost / impPricedKWh)
					notes = append(notes, "self_consumption_saving_gbp values the solar you used yourself at your average import rate over the period; the real saving depends on when you used it.")
				}
				summary["solar"] = sol
			}
		} else {
			notes = append(notes, "Octopus doesn't know how much your panels generated, so self-consumption isn't shown. Pass generated_kwh (from your inverter app, for the same period) to add it.")
		}
		if len(impReadings) == 0 && len(expReadings) == 0 {
			notes = append(notes, "No readings for this period. Smart meter data usually arrives the next day.")
		} else if len(expReadings) == 0 {
			notes = append(notes, "The export meter has no readings for this period. Export reads can start a few weeks after an export tariff begins.")
		}
		if truncated {
			notes = append(notes, fmt.Sprintf("Only the last %d rows are shown; use a coarser group_by or a shorter period to see everything.", maxRows))
		}

		out := Obj{
			"account_number": number,
			"import_meter":   Obj{"meter_point": imp.ID, "serial": impSerial},
			"export_meter":   Obj{"meter_point": exp.ID, "serial": expSerial},
			"from":           octopus.LocalTime(from),
			"to":             octopus.LocalTime(to),
			"group_by":       groupBy,
			"summary":        summary,
		}
		if rows != nil {
			out["rows"] = rows
		}
		if len(notes) > 0 {
			out["notes"] = notes
		}
		if len(errs) > 0 {
			out["errors"] = errs
		}
		return out, nil
	})
}
