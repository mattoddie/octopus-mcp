package tools

// octopus_find_greener_slots, in the tariffs toolset: the regional carbon
// intensity forecast from NESO's Carbon Intensity API, alongside prices.

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"sort"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
)

// carbonRegions maps the electricity region letters (GSP groups) to the region
// IDs of the Carbon Intensity API. Both are the 14 distribution network areas;
// the API's other IDs (15 to 17) are England, Scotland and Wales as a whole.
var carbonRegions = map[string]int{
	"P": 1,  // North Scotland
	"N": 2,  // South Scotland
	"G": 3,  // North West England
	"F": 4,  // North East England
	"M": 5,  // Yorkshire
	"D": 6,  // North Wales (and Merseyside)
	"K": 7,  // South Wales
	"E": 8,  // West Midlands
	"B": 9,  // East Midlands
	"A": 10, // East England
	"L": 11, // South West England
	"H": 12, // South England
	"C": 13, // London
	"J": 14, // South East England
}

type greenerArgs struct {
	TariffArgs
	From          string   `json:"from,omitempty" jsonschema:"Earliest start: a date and time, or now (default now)"`
	To            string   `json:"to,omitempty" jsonschema:"Latest end: a date and time, or a date (inclusive; default as far as the forecast goes, about 48 hours ahead)"`
	DurationHours *float64 `json:"duration_hours,omitempty" jsonschema:"Length of the continuous block to find, in hours, in steps of 0.5 (default 2)"`
	Slots         *int     `json:"slots,omitempty" jsonschema:"Also list this many of the lowest-carbon individual half-hours (default 6; 0 for none)"`
	KWh           *float64 `json:"kwh,omitempty" jsonschema:"Energy the job will use, in kWh, to estimate its emissions (and cost, when prices are known) in each block"`
}

// carbonTimeLayout is how the Carbon Intensity API writes and reads times.
const carbonTimeLayout = "2006-01-02T15:04Z07:00"

type carbonSlot struct {
	from, to  time.Time
	intensity float64 // gCO2/kWh, forecast
	index     string  // very low, low, moderate, high or very high
	price     float64 // p/kWh inc VAT, when priced
	priced    bool
}

func registerCarbon(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "tariffs", Name: "octopus_find_greener_slots", Title: "Find the greenest times",
		Description: "Find the lowest-carbon times to use electricity in your region over the next 48 hours, from the official regional carbon intensity forecast of NESO (Great Britain's electricity system operator, api.carbonintensity.org.uk), alongside your tariff's prices: the greenest continuous block of a given length, runner-up blocks, the greenest half-hours, the cheapest block for comparison, and the intensity now. " +
			"Intensity is grams of CO2 per kWh of electricity generated in the region (a forecast), with NESO's index from very low to very high. The region comes from the tariff, or from region or postcode on their own (then without prices). Choose the tariff by tariff_code, by product_code with region or postcode, or leave them out for the account's current tariff. Northern Ireland isn't covered.",
		Enums: tariffEnums,
	}, func(ctx context.Context, a greenerArgs) (any, error) {
		now := cl.Now()
		if a.From != "" {
			if f, err := parseWhen(a.From, false, now); err == nil && f.After(now.Add(48*time.Hour)) {
				return nil, fmt.Errorf("the carbon intensity forecast only goes about 48 hours ahead; choose a from before %s", octopus.LocalTime(now.Add(48*time.Hour)))
			}
		}
		from, to, err := parseRange(a.From, a.To, now, now.Add(48*time.Hour), now)
		if err != nil {
			return nil, err
		}
		dur := 2.0
		if a.DurationHours != nil {
			dur = *a.DurationHours
		}
		if dur <= 0 || dur > 24 || math.Mod(dur*2, 1) != 0 {
			return nil, fmt.Errorf("duration_hours must be a multiple of 0.5 between 0.5 and 24")
		}

		// The region, and the tariff to price slots with if there is one.
		var tariff *octopus.Tariff
		var region string
		info := Obj{}
		if a.TariffCode == "" && a.ProductCode == "" && (a.Region != "" || a.Postcode != "") {
			if region, err = regionArg(ctx, cl, a.Region, a.Postcode); err != nil {
				return nil, err
			}
		} else {
			t, source, err := resolveTariff(ctx, cl, a.TariffArgs)
			if err != nil {
				return nil, err
			}
			tariff, region, info = &t, t.Region, tariffInfo(t, source)
		}
		id, ok := carbonRegions[region]
		if !ok {
			return nil, fmt.Errorf("no carbon intensity region for %q", region)
		}

		var res Obj
		path := fmt.Sprintf("/regional/intensity/%s/fw48h/regionid/%d", url.PathEscape(halfHourFloor(from).UTC().Format(carbonTimeLayout)), id)
		if err := cl.CarbonIntensity(ctx, path, &res); err != nil {
			return nil, err
		}
		forecast := map[int64]carbonSlot{} // by start, in Unix seconds
		for _, d := range octopus.List(res, "data", "data") {
			f, err1 := time.Parse(carbonTimeLayout, octopus.Str(d, "from"))
			t, err2 := time.Parse(carbonTimeLayout, octopus.Str(d, "to"))
			v, ok := octopus.Num(d, "intensity", "forecast")
			if err1 == nil && err2 == nil && ok {
				forecast[f.Unix()] = carbonSlot{from: f, to: t, intensity: v, index: octopus.Str(d, "intensity", "index")}
			}
		}

		var notes []string
		var rates octopus.Rates
		if tariff != nil {
			switch {
			case tariff.Fuel != "electricity":
				notes = append(notes, "Carbon intensity is for electricity, so the gas tariff's prices aren't shown.")
				tariff = nil
			case tariff.DayNight():
				notes = append(notes, "Day/night tariffs aren't priced here, because Economy 7 hours vary by meter.")
				tariff = nil
			default:
				if rates, err = cl.TariffRates(ctx, *tariff, "standard-unit-rates", from.Add(-24*time.Hour), to); err != nil {
					return nil, err
				}
			}
		}

		// Whole half-hours inside [from, to) with a forecast, from the first one on.
		var slots []carbonSlot
		for s := halfHourFloor(from); !s.Add(30 * time.Minute).After(to); s = s.Add(30 * time.Minute) {
			if s.Before(from) {
				continue
			}
			cs, ok := forecast[s.Unix()]
			if !ok {
				if len(slots) > 0 {
					break
				}
				continue
			}
			if r, ok := rates.At(s); ok && tariff != nil {
				cs.price, cs.priced = r.IncVAT, true
			}
			slots = append(slots, cs)
		}
		n := int(dur * 2)
		if len(slots) < n {
			return nil, fmt.Errorf("the forecast has only %d half-hours between %s and %s, fewer than the %g hours asked for", len(slots), octopus.LocalTime(from), octopus.LocalTime(to), dur)
		}

		blockRow := func(start int) Obj {
			run := slots[start : start+n]
			var co2, cost float64
			allPriced := true
			for _, s := range run {
				co2 += s.intensity
				cost += s.price
				allPriced = allPriced && s.priced
			}
			co2 /= float64(n)
			o := Obj{"start": octopus.LocalTime(run[0].from), "end": octopus.LocalTime(run[n-1].to), "average_gco2_per_kwh": octopus.Round(co2, 0)}
			if allPriced {
				o["average_p_per_kwh"] = octopus.Round(cost/float64(n), 2)
			}
			if a.KWh != nil && *a.KWh > 0 {
				o["estimated_co2_kg"] = octopus.Round(co2**a.KWh/1000, 2)
				if allPriced {
					o["estimated_cost_gbp"] = pounds(cost / float64(n) * *a.KWh)
				}
			}
			return o
		}
		intensities := make([]float64, len(slots))
		for i, s := range slots {
			intensities[i] = s.intensity
		}
		var blocks []Obj
		for _, b := range bestBlocks(intensities, n, 3) {
			blocks = append(blocks, blockRow(b.start))
		}

		region = octopus.RegionLetter(region)
		out := Obj{
			"region":            Obj{"region": region, "region_name": octopus.Regions[region], "carbon_region_id": id, "carbon_region_name": octopus.Str(res, "data", "shortname")},
			"searched_from":     octopus.LocalTime(slots[0].from),
			"forecast_until":    octopus.LocalTime(slots[len(slots)-1].to),
			"duration_hours":    dur,
			"greenest_block":    blocks[0],
			"source":            "NESO Carbon Intensity API (carbonintensity.org.uk), regional forecast, CC BY 4.0",
			"intensity_units":   "gCO2/kWh",
			"average_in_window": octopus.Round(mean(intensities), 0),
		}
		if tariff != nil {
			out["tariff"] = info
		}
		if len(blocks) > 1 {
			out["alternatives"] = blocks[1:]
		}
		if cur, ok := forecast[halfHourFloor(now).Unix()]; ok {
			out["now"] = Obj{"start": octopus.LocalTime(cur.from), "gco2_per_kwh": octopus.Round(cur.intensity, 0), "index": cur.index}
		}
		// The cheapest block for comparison, over the slots whose prices are published.
		priced := 0
		for priced < len(slots) && slots[priced].priced {
			priced++
		}
		if tariff != nil && priced >= n {
			prices := make([]float64, priced)
			for i := range prices {
				prices[i] = slots[i].price
			}
			out["cheapest_block"] = blockRow(bestBlocks(prices, n, 1)[0].start)
			if priced < len(slots) {
				notes = append(notes, fmt.Sprintf("Prices are published until %s, so blocks after that show carbon only.", octopus.LocalTime(slots[priced-1].to)))
			}
		} else if tariff != nil {
			notes = append(notes, "Not enough prices are published in this window to compare costs.")
		}
		if want := limitArg(a.Slots, 6, 96); a.Slots == nil || *a.Slots > 0 {
			green := append([]carbonSlot(nil), slots...)
			sort.SliceStable(green, func(i, j int) bool { return green[i].intensity < green[j].intensity })
			if len(green) > want {
				green = green[:want]
			}
			sort.SliceStable(green, func(i, j int) bool { return green[i].from.Before(green[j].from) })
			var rows []Obj
			for _, s := range green {
				row := Obj{"start": octopus.LocalTime(s.from), "gco2_per_kwh": octopus.Round(s.intensity, 0), "index": s.index}
				if s.priced {
					row["p_per_kwh"] = octopus.Round(s.price, 2)
				}
				rows = append(rows, row)
			}
			out["greenest_half_hours"] = rows
		}
		if len(notes) > 0 {
			out["notes"] = notes
		}
		return out, nil
	})
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}
