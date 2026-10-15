package tools

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
)

// TariffArgs choose a tariff: by code, by product and region, or the account's current one.
type TariffArgs struct {
	TariffCode    string `json:"tariff_code,omitempty" jsonschema:"A tariff code like E-1R-AGILE-24-10-01-C (from octopus_get_product or octopus_get_account)"`
	ProductCode   string `json:"product_code,omitempty" jsonschema:"Instead of tariff_code: a product code like AGILE-24-10-01 (see octopus_list_products), with region or postcode"`
	Region        string `json:"region,omitempty" jsonschema:"With product_code: the region letter A to P (see octopus_find_region)"`
	Postcode      string `json:"postcode,omitempty" jsonschema:"With product_code, instead of region: a UK postcode, used to look up the region"`
	Fuel          string `json:"fuel,omitempty" jsonschema:"electricity (default) or gas"`
	AccountNumber string `json:"account_number,omitempty" jsonschema:"With none of the above, the account's current tariff is used: account number like A-1234ABCD (default OCTOPUS_ACCOUNT or the only account)"`
	Direction     string `json:"direction,omitempty" jsonschema:"With the account's tariff: import (default) or export"`
}

var tariffEnums = map[string][]string{"fuel": {"electricity", "gas"}, "direction": {"import", "export"}}

type productsArgs struct {
	Search          string `json:"search,omitempty" jsonschema:"Case-insensitive filter on the product code and names, for example agile, go, cosy, tracker, flux or fixed"`
	Direction       string `json:"direction,omitempty" jsonschema:"Only import or export products"`
	Variable        *bool  `json:"variable,omitempty" jsonschema:"true for variable-price products only, false for fixed-price only"`
	IncludeBusiness bool   `json:"include_business,omitempty" jsonschema:"Include business products (default household only)"`
	AvailableAt     string `json:"available_at,omitempty" jsonschema:"Products on sale at this date (YYYY-MM-DD; default now). Use a past date to find products that are no longer sold"`
	Brand           string `json:"brand,omitempty" jsonschema:"Brand code (default OCTOPUS_ENERGY)"`
}

type productArgs struct {
	ProductCode string `json:"product_code" jsonschema:"Product code, like AGILE-24-10-01 or VAR-22-11-01 (see octopus_list_products)"`
	Region      string `json:"region,omitempty" jsonschema:"Only show tariffs for this region letter, A to P (default all regions)"`
	Postcode    string `json:"postcode,omitempty" jsonschema:"Instead of region: a UK postcode, used to look up the region"`
	At          string `json:"at,omitempty" jsonschema:"Show the prices in force at this date (YYYY-MM-DD; default now)"`
	Raw         bool   `json:"raw,omitempty" jsonschema:"Return the API response unchanged"`
}

type ratesArgs struct {
	TariffArgs
	From    string `json:"from,omitempty" jsonschema:"Start: a date (YYYY-MM-DD, UK time), a date and time, or now/today/tomorrow (default the start of today)"`
	To      string `json:"to,omitempty" jsonschema:"End: a date (inclusive, the whole day), a date and time, or today/tomorrow (default the end of tomorrow; Agile prices for tomorrow are usually published around 4pm)"`
	MaxRows *int   `json:"max_rows,omitempty" jsonschema:"Return at most this many rates (default 200)"`
}

type standingArgs struct {
	TariffArgs
	From string `json:"from,omitempty" jsonschema:"Start of the history to show (YYYY-MM-DD; default 1 year ago)"`
	To   string `json:"to,omitempty" jsonschema:"End of the history (YYYY-MM-DD, inclusive; default today)"`
}

type cheapestArgs struct {
	TariffArgs
	From          string   `json:"from,omitempty" jsonschema:"Earliest start: a date and time, or now (default now)"`
	To            string   `json:"to,omitempty" jsonschema:"Latest end: a date and time, or a date (inclusive; default as far ahead as prices are published)"`
	DurationHours *float64 `json:"duration_hours,omitempty" jsonschema:"Length of the continuous block to find, in hours, in steps of 0.5 (default 2)"`
	Slots         *int     `json:"slots,omitempty" jsonschema:"Also list this many of the cheapest individual half-hours, which need not be together (default 6; 0 for none)"`
	KWh           *float64 `json:"kwh,omitempty" jsonschema:"Energy the job will use, in kWh (for example 7 for a dishwasher and washing machine), to estimate its cost in the best block"`
}

type regionArgs struct {
	Postcode string `json:"postcode" jsonschema:"A UK postcode, for example SW1A 1AA"`
}

// findRegion looks up a postcode's electricity region letter.
func findRegion(ctx context.Context, cl *octopus.Client, postcode string) (string, error) {
	pc := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(postcode), " ", ""))
	if pc == "" {
		return "", fmt.Errorf("give a postcode")
	}
	var res Obj
	if err := cl.GetPublic(ctx, "/industry/grid-supply-points/", url.Values{"postcode": {pc}}, &res); err != nil {
		return "", err
	}
	var letters []string
	for _, r := range octopus.List(res, "results") {
		if l := octopus.RegionLetter(octopus.Str(r, "group_id")); l != "" {
			letters = append(letters, l)
		}
	}
	letters = unique(letters)
	switch len(letters) {
	case 0:
		return "", fmt.Errorf("Octopus doesn't know which region %s is in; check the postcode, or pass region instead", postcode)
	case 1:
		return letters[0], nil
	}
	return "", fmt.Errorf("%s spans several regions (%s); pass region instead", postcode, strings.Join(letters, ", "))
}

func regionArg(ctx context.Context, cl *octopus.Client, region, postcode string) (string, error) {
	if region != "" {
		l := octopus.RegionLetter(region)
		if l == "" {
			return "", fmt.Errorf("%q is not a region; use a letter from A to P (not I or O), or a postcode", region)
		}
		return l, nil
	}
	if postcode != "" {
		return findRegion(ctx, cl, postcode)
	}
	return "", nil
}

// resolveTariff turns tariff arguments into a tariff, and says where it came from.
func resolveTariff(ctx context.Context, cl *octopus.Client, a TariffArgs) (octopus.Tariff, Obj, error) {
	switch {
	case a.TariffCode != "":
		t, err := octopus.ParseTariff(a.TariffCode)
		return t, Obj{"source": "tariff_code"}, err
	case a.ProductCode != "":
		region, err := regionArg(ctx, cl, a.Region, a.Postcode)
		if err != nil {
			return octopus.Tariff{}, nil, err
		}
		if region == "" {
			return octopus.Tariff{}, nil, fmt.Errorf("with product_code, also give region (A to P) or postcode")
		}
		fuel := strings.ToLower(a.Fuel)
		if fuel == "" {
			fuel = "electricity"
		}
		t, err := octopus.ParseTariff(octopus.TariffCode(fuel, "1R", a.ProductCode, region))
		return t, Obj{"source": "product_code"}, err
	}
	_, _, mps, err := loadAccount(ctx, cl, a.AccountNumber)
	if err != nil {
		return octopus.Tariff{}, nil, fmt.Errorf("no tariff_code or product_code was given, so the account's tariff was needed: %w", err)
	}
	mp, err := pickMeterPoint(mps, MeterArgs{Fuel: a.Fuel, Direction: a.Direction})
	if err != nil {
		return octopus.Tariff{}, nil, err
	}
	ag := mp.agreementAt(cl.Now())
	if ag == nil {
		return octopus.Tariff{}, nil, fmt.Errorf("%s has no tariff active today", mp.label())
	}
	return ag.Tariff, Obj{"source": "account", "meter_point": mp.ID}, nil
}

func tariffInfo(t octopus.Tariff, source Obj) Obj {
	out := Obj{"tariff_code": t.Code, "product_code": t.Product, "fuel": t.Fuel, "region": t.Region, "region_name": octopus.Regions[t.Region]}
	for k, v := range source {
		out[k] = v
	}
	if t.DayNight() {
		out["day_night"] = true
	}
	return out
}

func rateRow(r octopus.Rate) Obj {
	o := Obj{"from": octopus.LocalTime(r.From), "p_per_kwh": octopus.Round(r.IncVAT, 2), "exc_vat": octopus.Round(r.ExcVAT, 2)}
	if !r.To.IsZero() {
		o["to"] = octopus.LocalTime(r.To)
	} else {
		o["to"] = nil
	}
	return o
}

// clip limits rates to [from, to), trimming the first and last to the window.
func clip(rs octopus.Rates, from, to time.Time) octopus.Rates {
	var out octopus.Rates
	for _, r := range rs {
		if !r.From.Before(to) || (!r.To.IsZero() && !r.To.After(from)) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func rateStats(rs octopus.Rates, now time.Time) Obj {
	if len(rs) == 0 {
		return Obj{}
	}
	lo, hi, sum, n := math.Inf(1), math.Inf(-1), 0.0, 0
	negative := 0
	for _, r := range rs {
		lo, hi = math.Min(lo, r.IncVAT), math.Max(hi, r.IncVAT)
		sum += r.IncVAT
		n++
		if r.IncVAT < 0 {
			negative++
		}
	}
	out := Obj{"min_p_per_kwh": octopus.Round(lo, 2), "max_p_per_kwh": octopus.Round(hi, 2), "average_p_per_kwh": octopus.Round(sum/float64(n), 2)}
	if negative > 0 {
		out["negative_price_periods"] = negative
		out["note"] = "Negative prices mean Octopus pays you to use electricity in those periods."
	}
	if cur, ok := rs.At(now); ok {
		out["now"] = rateRow(cur)
		for _, r := range rs {
			if r.From.After(now) && r.IncVAT != cur.IncVAT {
				out["next_change"] = rateRow(r)
				break
			}
		}
	}
	if last := rs[len(rs)-1]; !last.To.IsZero() {
		out["published_until"] = octopus.LocalTime(last.To)
	}
	return out
}

func summarizeProduct(p Obj) Obj {
	return octopus.Compact(Obj{
		"code":           octopus.Str(p, "code"),
		"name":           octopus.Str(p, "display_name"),
		"full_name":      octopus.Str(p, "full_name"),
		"direction":      strings.ToLower(octopus.Str(p, "direction")),
		"variable":       octopus.Bool(p, "is_variable"),
		"tracker":        octopus.Bool(p, "is_tracker"),
		"green":          octopus.Bool(p, "is_green"),
		"prepay":         octopus.Bool(p, "is_prepay"),
		"business":       octopus.Bool(p, "is_business"),
		"restricted":     octopus.Bool(p, "is_restricted"),
		"term_months":    octopus.Get(p, "term"),
		"available_from": octopus.Str(p, "available_from"),
		"available_to":   octopus.Str(p, "available_to"),
	})
}

// productTariffs flattens a product's tariff maps into rows, one per tariff code.
func productTariffs(p Obj, region string) []Obj {
	kinds := []struct{ key, label string }{
		{"single_register_electricity_tariffs", "electricity"},
		{"dual_register_electricity_tariffs", "electricity day/night (Economy 7)"},
		{"single_register_gas_tariffs", "gas"},
	}
	var out []Obj
	for _, k := range kinds {
		byRegion, _ := p[k.key].(Obj)
		regions := make([]string, 0, len(byRegion))
		for r := range byRegion {
			regions = append(regions, r)
		}
		sort.Strings(regions)
		for _, r := range regions {
			letter := octopus.RegionLetter(r)
			if region != "" && letter != region {
				continue
			}
			methods, _ := byRegion[r].(Obj)
			// Prefer direct debit prices, which most customers pay.
			t, _ := methods["direct_debit_monthly"].(Obj)
			method := "direct_debit_monthly"
			if t == nil {
				for m, v := range methods {
					if o, ok := v.(Obj); ok {
						t, method = o, m
						break
					}
				}
			}
			if t == nil {
				continue
			}
			row := octopus.Compact(Obj{
				"type":                      k.label,
				"region":                    letter,
				"region_name":               octopus.Regions[letter],
				"tariff_code":               octopus.Str(t, "code"),
				"unit_rate_p_per_kwh":       octopus.NumOrNil(t, 2, "standard_unit_rate_inc_vat"),
				"day_rate_p_per_kwh":        octopus.NumOrNil(t, 2, "day_unit_rate_inc_vat"),
				"night_rate_p_per_kwh":      octopus.NumOrNil(t, 2, "night_unit_rate_inc_vat"),
				"standing_charge_p_per_day": octopus.NumOrNil(t, 2, "standing_charge_inc_vat"),
				"exit_fee_gbp":              octopus.Pence(t, "exit_fees_inc_vat"),
				"payment_method":            method,
			})
			if v, _ := octopus.Num(t, "exit_fees_inc_vat"); v == 0 {
				delete(row, "exit_fee_gbp")
			}
			out = append(out, row)
		}
	}
	return out
}

func registerTariffs(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "tariffs", Name: "octopus_list_products", Title: "List Octopus products",
		Description: "List the energy products (tariffs) Octopus Energy sells now, such as Agile, Go, Intelligent Go, Cosy, Tracker, Flux, Flexible and fixed deals, with whether each is variable, a tracker, green, import or export. Public data; no account needed. Use the codes with octopus_get_product for prices.",
		Enums:       map[string][]string{"direction": {"import", "export"}},
	}, func(ctx context.Context, a productsArgs) (any, error) {
		brand := a.Brand
		if brand == "" {
			brand = "OCTOPUS_ENERGY"
		}
		q := url.Values{"brand": {brand}, "page_size": {"500"}}
		if !a.IncludeBusiness {
			q.Set("is_business", "false")
		}
		if a.AvailableAt != "" {
			t, err := parseWhen(a.AvailableAt, false, cl.Now())
			if err != nil {
				return nil, err
			}
			q.Set("available_at", octopus.FormatTime(t))
		}
		page, err := cl.GetPages(ctx, "/products/", q, false, 2000)
		if err != nil {
			return nil, err
		}
		search := strings.ToLower(a.Search)
		items := []Obj{}
		for _, p := range page.Results {
			s := summarizeProduct(p)
			if a.Direction != "" && s["direction"] != strings.ToLower(a.Direction) {
				continue
			}
			if a.Variable != nil && octopus.Bool(p, "is_variable") != *a.Variable {
				continue
			}
			if search != "" && !strings.Contains(strings.ToLower(octopus.Str(p, "code")+" "+octopus.Str(p, "display_name")+" "+octopus.Str(p, "full_name")), search) {
				continue
			}
			items = append(items, s)
		}
		octopus.SortBy(items, "code")
		return Obj{"total": len(items), "items": items}, nil
	})

	add(c, Def{
		Toolset: "tariffs", Name: "octopus_get_product", Title: "Get product and prices",
		Description: "Get an Octopus product's details and its current prices in each region, including VAT: unit rate (pence per kWh; day and night rates for Economy 7), standing charge (pence per day) and any exit fee, for electricity and gas. " +
			"For half-hourly tariffs such as Agile the unit rate shown is only indicative; use octopus_get_unit_rates. Public data; no account needed.",
	}, func(ctx context.Context, a productArgs) (any, error) {
		region, err := regionArg(ctx, cl, a.Region, a.Postcode)
		if err != nil {
			return nil, err
		}
		q := url.Values{}
		if a.At != "" {
			t, err := parseWhen(a.At, false, cl.Now())
			if err != nil {
				return nil, err
			}
			q.Set("tariffs_active_at", octopus.FormatTime(t))
		}
		var p Obj
		if err := cl.GetPublic(ctx, "/products/"+url.PathEscape(strings.ToUpper(strings.TrimSpace(a.ProductCode)))+"/", q, &p); err != nil {
			return nil, err
		}
		if a.Raw {
			return p, nil
		}
		out := summarizeProduct(p)
		out["description"] = octopus.Str(p, "description")
		out["prices_at"] = octopus.Str(p, "tariffs_active_at")
		tariffs := productTariffs(p, region)
		if len(tariffs) == 0 {
			out["note"] = "No tariffs are available for this product at that date (and region)."
		}
		out["tariffs"] = tariffs
		if region != "" {
			if quotes, ok := octopus.Get(p, "sample_quotes", "_"+region, "direct_debit_monthly").(Obj); ok {
				sq := Obj{}
				for k := range quotes {
					sq[k+"_gbp_per_year"] = octopus.Pence(quotes, k, "annual_cost_inc_vat")
				}
				out["sample_annual_costs"] = sq
				out["sample_consumption_kwh"] = p["sample_consumption"]
			}
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "tariffs", Name: "octopus_get_unit_rates", Title: "Unit rates",
		Description: "Get a tariff's unit rates (pence per kWh, including VAT) over a period: every half-hour for Agile, the cheap and peak periods for Go, Intelligent Go, Cosy and Flux, daily prices for Tracker, and the flat rate for fixed and flexible tariffs. " +
			"Also gives the price now, when it next changes, and the minimum, maximum and average. Choose the tariff by tariff_code, by product_code with region or postcode, or leave them out for the account's current tariff. Day/night tariffs list day and night rates separately.",
		Enums: tariffEnums,
	}, func(ctx context.Context, a ratesArgs) (any, error) {
		now := cl.Now()
		today := startOfDay(now)
		from, to, err := parseRange(a.From, a.To, today, today.AddDate(0, 0, 2), now)
		if err != nil {
			return nil, err
		}
		if to.Sub(from) > 93*24*time.Hour {
			return nil, fmt.Errorf("ask for at most three months of rates at a time")
		}
		t, source, err := resolveTariff(ctx, cl, a.TariffArgs)
		if err != nil {
			return nil, err
		}
		out := Obj{"tariff": tariffInfo(t, source), "from": octopus.LocalTime(from), "to": octopus.LocalTime(to)}
		maxRows := limitArg(a.MaxRows, 200, 2000)
		list := func(kind string) (Obj, error) {
			rs, err := cl.TariffRates(ctx, t, kind, from, to)
			if err != nil {
				return nil, err
			}
			rs = clip(rs, from, to)
			res := Obj{"summary": rateStats(rs, now)}
			rows := make([]Obj, 0, len(rs))
			for _, r := range rs {
				rows = append(rows, rateRow(r))
			}
			if len(rows) > maxRows {
				rows = rows[:maxRows]
				res["truncated"] = true
			}
			res["rates"] = rows
			return res, nil
		}
		if t.DayNight() {
			day, err := list("day-unit-rates")
			if err != nil {
				return nil, err
			}
			night, err := list("night-unit-rates")
			if err != nil {
				return nil, err
			}
			out["day"], out["night"] = day, night
			out["note"] = "Economy 7 night hours vary by meter and region (often 7 hours between midnight and 08:30 GMT); Octopus's API doesn't say which hours your meter uses."
			return out, nil
		}
		res, err := list("standard-unit-rates")
		if err != nil {
			return nil, err
		}
		for k, v := range res {
			out[k] = v
		}
		if len(res["rates"].([]Obj)) == 0 {
			out["note"] = "No rates were published for this period. Agile prices for tomorrow usually appear around 4pm; check the tariff code and region."
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "tariffs", Name: "octopus_get_standing_charges", Title: "Standing charges",
		Description: "Get a tariff's daily standing charge (pence per day, including VAT): the current charge and how it has changed over a period. Choose the tariff by tariff_code, by product_code with region or postcode, or leave them out for the account's current tariff.",
		Enums:       tariffEnums,
	}, func(ctx context.Context, a standingArgs) (any, error) {
		now := cl.Now()
		today := startOfDay(now)
		from, to, err := parseRange(a.From, a.To, today.AddDate(-1, 0, 0), today.AddDate(0, 0, 1), now)
		if err != nil {
			return nil, err
		}
		t, source, err := resolveTariff(ctx, cl, a.TariffArgs)
		if err != nil {
			return nil, err
		}
		rs, err := cl.TariffRates(ctx, t, "standing-charges", from, to)
		if err != nil {
			return nil, err
		}
		rs = clip(rs, from, to)
		rows := []Obj{}
		for _, r := range rs {
			row := rateRow(r)
			row["p_per_day"], row["exc_vat"] = octopus.Round(r.IncVAT, 2), octopus.Round(r.ExcVAT, 2)
			delete(row, "p_per_kwh")
			rows = append(rows, row)
		}
		out := Obj{"tariff": tariffInfo(t, source), "charges": rows}
		if cur, ok := rs.At(now); ok {
			out["current_p_per_day"] = octopus.Round(cur.IncVAT, 2)
			out["current_gbp_per_year"] = pounds(cur.IncVAT * 365)
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "tariffs", Name: "octopus_find_cheapest_slots", Title: "Find the cheapest times",
		Description: "Find the cheapest time to use electricity on a time-of-use tariff such as Agile: the cheapest continuous block of a given length (for a dishwasher, washing machine, battery or car), runner-up blocks that don't overlap, and the cheapest individual half-hours. " +
			"Only looks at prices Octopus has published (Agile prices for the next day usually appear around 4pm). Choose the tariff by tariff_code, by product_code with region or postcode, or leave them out for the account's current tariff.",
		Enums: tariffEnums,
	}, func(ctx context.Context, a cheapestArgs) (any, error) {
		now := cl.Now()
		from, to, err := parseRange(a.From, a.To, now, startOfDay(now).AddDate(0, 0, 2), now)
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
		t, source, err := resolveTariff(ctx, cl, a.TariffArgs)
		if err != nil {
			return nil, err
		}
		if t.DayNight() {
			return nil, fmt.Errorf("%s is a day/night tariff: electricity is cheapest in its night period (Economy 7 hours vary by meter)", t.Code)
		}
		rs, err := cl.TariffRates(ctx, t, "standard-unit-rates", from.Add(-24*time.Hour), to)
		if err != nil {
			return nil, err
		}
		// Half-hour slots from the first whole half-hour, up to where prices are known.
		var slots []octopus.Rate
		for s := halfHourFloor(from); s.Before(to); s = s.Add(30 * time.Minute) {
			if s.Before(from) {
				continue
			}
			r, ok := rs.At(s)
			if !ok {
				break
			}
			slots = append(slots, octopus.Rate{From: s, To: s.Add(30 * time.Minute), IncVAT: r.IncVAT, ExcVAT: r.ExcVAT})
		}
		n := int(dur * 2)
		out := Obj{"tariff": tariffInfo(t, source), "searched_from": octopus.LocalTime(from), "duration_hours": dur}
		if len(slots) > 0 {
			out["prices_known_until"] = octopus.LocalTime(slots[len(slots)-1].To)
		}
		if len(slots) < n {
			return nil, fmt.Errorf("only %d half-hours of prices are published between %s and %s, fewer than the %g hours asked for", len(slots), octopus.LocalTime(from), octopus.LocalTime(to), dur)
		}
		avgs := make([]float64, len(slots)-n+1)
		for i := range avgs {
			sum := 0.0
			for _, s := range slots[i : i+n] {
				sum += s.IncVAT
			}
			avgs[i] = sum / float64(n)
		}
		order := make([]int, len(avgs))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(x, y int) bool { return avgs[order[x]] < avgs[order[y]] })
		var blocks []Obj
		var taken []int
		for _, i := range order {
			overlaps := false
			for _, j := range taken {
				if i < j+n && j < i+n {
					overlaps = true
					break
				}
			}
			if overlaps {
				continue
			}
			taken = append(taken, i)
			b := Obj{"start": octopus.LocalTime(slots[i].From), "end": octopus.LocalTime(slots[i+n-1].To), "average_p_per_kwh": octopus.Round(avgs[i], 2)}
			if a.KWh != nil && *a.KWh > 0 {
				b["estimated_cost_gbp"] = pounds(avgs[i] * *a.KWh)
			}
			blocks = append(blocks, b)
			if len(blocks) == 3 {
				break
			}
		}
		out["best_block"] = blocks[0]
		if len(blocks) > 1 {
			out["alternatives"] = blocks[1:]
		}
		overall := 0.0
		for _, s := range slots {
			overall += s.IncVAT
		}
		overall /= float64(len(slots))
		out["average_p_per_kwh_in_window"] = octopus.Round(overall, 2)
		if a.KWh != nil && *a.KWh > 0 {
			if r, ok := rs.At(now); ok {
				out["cost_if_run_now_gbp"] = pounds(r.IncVAT * *a.KWh)
			}
		}
		if want := limitArg(a.Slots, 6, 96); a.Slots == nil || *a.Slots > 0 {
			cheapest := append([]octopus.Rate(nil), slots...)
			sort.SliceStable(cheapest, func(i, j int) bool { return cheapest[i].IncVAT < cheapest[j].IncVAT })
			if len(cheapest) > want {
				cheapest = cheapest[:want]
			}
			sort.SliceStable(cheapest, func(i, j int) bool { return cheapest[i].From.Before(cheapest[j].From) })
			var rows []Obj
			for _, s := range cheapest {
				rows = append(rows, Obj{"start": octopus.LocalTime(s.From), "p_per_kwh": octopus.Round(s.IncVAT, 2)})
			}
			out["cheapest_half_hours"] = rows
		}
		lo, hi := slots[0].IncVAT, slots[0].IncVAT
		for _, s := range slots {
			lo, hi = math.Min(lo, s.IncVAT), math.Max(hi, s.IncVAT)
		}
		if lo == hi {
			out["note"] = "The price is the same throughout this window, so timing makes no difference on this tariff."
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "tariffs", Name: "octopus_find_region", Title: "Find region by postcode",
		Description: "Find which of the 14 electricity regions (A to P) a UK postcode is in. Tariff prices differ by region, and tariff codes end with the region letter. Public data; no account needed.",
	}, func(ctx context.Context, a regionArgs) (any, error) {
		l, err := findRegion(ctx, cl, a.Postcode)
		if err != nil {
			return nil, err
		}
		return Obj{"postcode": strings.ToUpper(strings.TrimSpace(a.Postcode)), "region": l, "region_name": octopus.Regions[l], "gsp_group": "_" + l}, nil
	})
}
