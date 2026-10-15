package tools

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
)

type consumptionArgs struct {
	MeterArgs
	From           string   `json:"from,omitempty" jsonschema:"Start: a date (YYYY-MM-DD, UK time), a date and time, or today/yesterday (default 7 days ago)"`
	To             string   `json:"to,omitempty" jsonschema:"End: a date (inclusive, the whole day), a date and time, or now/today/yesterday (default the end of yesterday)"`
	GroupBy        string   `json:"group_by,omitempty" jsonschema:"How to total the half-hourly readings: half_hour, hour, day (default), week (Monday to Sunday), month or total"`
	IncludeCost    *bool    `json:"include_cost,omitempty" jsonschema:"Estimate the cost (or export earnings) from the tariff's actual unit rates and standing charges (default true)"`
	MaxRows        *int     `json:"max_rows,omitempty" jsonschema:"Return at most this many rows, the most recent (default 400); use a coarser group_by for long periods"`
	GasUnits       string   `json:"gas_units,omitempty" jsonschema:"Override the units a gas meter reports in: kwh or m3 (default: as Octopus says for the meter, otherwise m3, which SMETS2 meters use)"`
	CalorificValue *float64 `json:"calorific_value,omitempty" jsonschema:"Gas calorific value in MJ/m³ for converting m³ to kWh (default 39.5; your bill shows the actual value)"`
}

type liveArgs struct {
	AccountNumber string `json:"account_number,omitempty" jsonschema:"Account number like A-1234ABCD (default OCTOPUS_ACCOUNT, or the only account the API key can see)"`
	DeviceID      string `json:"device_id,omitempty" jsonschema:"Smart meter device ID (default: the electricity import meter's, from octopus_get_account)"`
	Minutes       *int   `json:"minutes,omitempty" jsonschema:"How far back to look, in minutes (default 60, at most 1440)"`
	Grouping      string `json:"grouping,omitempty" jsonschema:"Reading interval: TEN_SECONDS, ONE_MINUTE, FIVE_MINUTES, HALF_HOURLY or HOURLY (default ONE_MINUTE up to an hour, FIVE_MINUTES beyond)"`
	MaxRows       *int   `json:"max_rows,omitempty" jsonschema:"Return at most this many readings, the most recent (default 60)"`
}

const (
	// Standard gas conversion: volume correction factor and MJ per kWh.
	gasVolumeCorrection = 1.02264
	mjPerKWh            = 3.6
	defaultCV           = 39.5
	maxConsumptionDays  = 732
)

type reading struct {
	Start, End time.Time
	Value      float64 // as reported: kWh, or m³ for SMETS2 gas
}

// fetchConsumption gets the half-hourly readings of one meter, oldest first.
func fetchConsumption(ctx context.Context, cl *octopus.Client, mp *meterPoint, serial string, from, to time.Time) ([]reading, error) {
	kind := "electricity-meter-points"
	if mp.Fuel == "gas" {
		kind = "gas-meter-points"
	}
	path := fmt.Sprintf("/%s/%s/meters/%s/consumption/", kind, url.PathEscape(mp.ID), url.PathEscape(serial))
	q := url.Values{
		"period_from": {octopus.FormatTime(from)},
		"period_to":   {octopus.FormatTime(to)},
		"page_size":   {"25000"},
		"order_by":    {"period"},
	}
	page, err := cl.GetPages(ctx, path, q, true, 40000)
	if err != nil {
		return nil, err
	}
	out := make([]reading, 0, len(page.Results))
	for _, r := range page.Results {
		s, ok1 := octopus.ParseTime(octopus.Str(r, "interval_start"))
		e, ok2 := octopus.ParseTime(octopus.Str(r, "interval_end"))
		v, ok3 := octopus.Num(r, "consumption")
		if ok1 && ok2 && ok3 {
			out = append(out, reading{Start: s, End: e, Value: v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

// groupStart returns the start of the group a reading belongs to, in UK time.
func groupStart(t time.Time, by string) time.Time {
	l := t.In(octopus.London)
	y, m, d := l.Date()
	switch by {
	case "half_hour":
		return l
	case "hour":
		return time.Date(y, m, d, l.Hour(), 0, 0, 0, octopus.London)
	case "week":
		offset := (int(l.Weekday()) + 6) % 7 // Monday = 0
		return time.Date(y, m, d-offset, 0, 0, 0, 0, octopus.London)
	case "month":
		return time.Date(y, m, 1, 0, 0, 0, 0, octopus.London)
	case "total":
		return time.Time{}
	}
	return time.Date(y, m, d, 0, 0, 0, 0, octopus.London)
}

func groupLabel(t time.Time, by string) string {
	switch by {
	case "day", "week":
		return t.Format("2006-01-02")
	case "month":
		return t.Format("2006-01")
	}
	return t.Format(time.RFC3339)
}

// gasUnitsFor decides whether a gas meter reports m³ or kWh.
func gasUnitsFor(override, fromOctopus string) (units string, assumed bool) {
	switch strings.ToLower(strings.TrimSpace(override)) {
	case "kwh":
		return "kWh", false
	case "m3", "m³":
		return "m³", false
	}
	u := strings.ToLower(fromOctopus)
	switch {
	case strings.Contains(u, "kwh"):
		return "kWh", false
	case strings.Contains(u, "m3") || strings.Contains(u, "m³") || strings.Contains(u, "cub"):
		return "m³", false
	}
	return "m³", true
}

func registerConsumption(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "consumption", Name: "octopus_get_consumption", Title: "Energy consumption and cost",
		Description: "Get smart meter readings for electricity (import or export) or gas, totalled by half-hour, hour, day, week or month in UK time, with an estimated cost from the tariff that was active at the time: the actual unit rate for every half-hour (so Agile, Go and other time-of-use tariffs are priced correctly) plus standing charges, including VAT. " +
			"Readings come from the smart meter, usually a day behind; there is no data for meters that aren't smart or don't send half-hourly reads. " +
			"Electricity is in kWh. SMETS2 gas meters report m³, which is converted to kWh for costing with the standard formula (m³ × 1.02264 × calorific value ÷ 3.6). For export meters the estimate is what Octopus pays you. " +
			"Day/night (Economy 7) tariffs are priced assuming night hours of 00:30 to 07:30 GMT, which may not match your meter. Estimates exclude discounts and credits; your bill is the source of truth.",
		Enums: merge(meterEnums, map[string][]string{"group_by": {"half_hour", "hour", "day", "week", "month", "total"}, "gas_units": {"kwh", "m3"}}),
	}, func(ctx context.Context, a consumptionArgs) (any, error) {
		now := cl.Now()
		today := startOfDay(now)
		from, to, err := parseRange(a.From, a.To, today.AddDate(0, 0, -7), today, now)
		if err != nil {
			return nil, err
		}
		if to.Sub(from) > maxConsumptionDays*24*time.Hour {
			return nil, fmt.Errorf("the period is longer than two years; ask for less at a time")
		}
		groupBy := strings.ToLower(a.GroupBy)
		if groupBy == "" {
			groupBy = "day"
		}
		number, _, mps, err := loadAccount(ctx, cl, a.AccountNumber)
		if err != nil {
			return nil, err
		}
		mp, err := pickMeterPoint(mps, a.MeterArgs)
		if err != nil {
			return nil, err
		}
		serials := mp.Serials
		if a.Serial != "" {
			serials = []string{a.Serial}
		}
		if len(serials) == 0 {
			return nil, fmt.Errorf("%s has no meters listed", mp.label())
		}
		// A meter point can list old and new meters; use whichever has readings.
		var readings []reading
		serial := serials[0]
		var lastErr error
		for _, s := range serials {
			r, err := fetchConsumption(ctx, cl, mp, s, from, to)
			if err != nil {
				lastErr = err
				continue
			}
			if len(r) > len(readings) {
				readings, serial = r, s
			}
		}
		if readings == nil && lastErr != nil {
			return nil, lastErr
		}

		var assumptions []string
		units, perKWh := "kWh", 1.0
		if mp.Fuel == "gas" {
			fromOctopus := ""
			if a.GasUnits == "" {
				if devices, err := loadSmartDevices(ctx, cl, number); err == nil {
					fromOctopus = devices[serial].Units
				}
			}
			var assumed bool
			units, assumed = gasUnitsFor(a.GasUnits, fromOctopus)
			if assumed {
				assumptions = append(assumptions, "Octopus didn't say which units this gas meter reports in, so m³ (SMETS2) was assumed. If the meter is SMETS1 its readings are already kWh: call again with gas_units=kwh.")
			}
			if units == "m³" {
				cv := defaultCV
				if a.CalorificValue != nil && *a.CalorificValue > 0 {
					cv = *a.CalorificValue
				}
				perKWh = gasVolumeCorrection * cv / mjPerKWh
				assumptions = append(assumptions, fmt.Sprintf("Gas m³ converted to kWh with calorific value %.1f MJ/m³ (×%.3f).", cv, perKWh))
			}
		}

		includeCost := a.IncludeCost == nil || *a.IncludeCost
		costWord := "cost_gbp"
		if mp.Export {
			costWord = "earnings_gbp"
		}
		lookups := map[string]*octopus.UnitRateLookup{}
		var costErrs []string
		priceAt := func(t time.Time) (float64, bool) {
			ag := mp.agreementAt(t)
			if ag == nil {
				return 0, false
			}
			l, ok := lookups[ag.Tariff.Code]
			if !ok {
				lf, lt := maxTime(from, ag.From), to
				if !ag.To.IsZero() && ag.To.Before(lt) {
					lt = ag.To
				}
				var err error
				l, err = cl.LoadUnitRates(ctx, ag.Tariff, lf, lt)
				if err != nil {
					costErrs = append(costErrs, ag.Tariff.Code+": "+err.Error())
					l = nil
				}
				lookups[ag.Tariff.Code] = l
				if l != nil && ag.Tariff.DayNight() {
					assumptions = append(assumptions, "Day/night tariff priced with night hours 00:30 to 07:30 GMT; Economy 7 hours vary by meter.")
				}
			}
			if l == nil {
				return 0, false
			}
			r, ok := l.At(t)
			return r.IncVAT, ok
		}

		type group struct {
			start            time.Time
			value, kwh, cost float64
			n, priced        int
		}
		var groups []*group
		index := map[time.Time]*group{}
		total := &group{}
		var peak reading
		for _, r := range readings {
			k := groupStart(r.Start, groupBy)
			g, ok := index[k]
			if !ok {
				g = &group{start: k}
				index[k] = g
				groups = append(groups, g)
			}
			kwh := r.Value * perKWh
			for _, x := range []*group{g, total} {
				x.value += r.Value
				x.kwh += kwh
				x.n++
			}
			if r.Value > peak.Value {
				peak = r
			}
			if includeCost {
				if p, ok := priceAt(r.Start); ok {
					for _, x := range []*group{g, total} {
						x.cost += kwh * p
						x.priced++
					}
				}
			}
		}

		rows := make([]Obj, 0, len(groups))
		for _, g := range groups {
			row := Obj{"start": groupLabel(g.start, groupBy), units: octopus.Round(g.value, 3)}
			if units != "kWh" {
				row["kwh"] = octopus.Round(g.kwh, 2)
			}
			if groupBy != "half_hour" {
				row["half_hours"] = g.n
			}
			if includeCost && g.priced > 0 {
				row[costWord] = pounds(g.cost)
				if g.kwh > 0 && groupBy != "half_hour" {
					row["avg_p_per_kwh"] = octopus.Round(g.cost/g.kwh, 2)
				}
			}
			rows = append(rows, row)
		}
		if groupBy == "total" {
			rows = nil
		}
		maxRows := limitArg(a.MaxRows, 400, 5000)
		truncated := false
		if len(rows) > maxRows {
			rows = rows[len(rows)-maxRows:]
			truncated = true
		}

		expected := int(to.Sub(from) / (30 * time.Minute))
		if end := halfHourFloor(now); to.After(end) {
			expected = int(math.Max(0, float64(end.Sub(from)/(30*time.Minute))))
		}
		summary := Obj{
			units:                  octopus.Round(total.value, 3),
			"half_hours_with_data": total.n,
			"half_hours_in_period": expected,
		}
		if units != "kWh" {
			summary["kwh"] = octopus.Round(total.kwh, 2)
		}
		if peak.Value > 0 {
			summary["peak_half_hour"] = Obj{"start": octopus.LocalTime(peak.Start), units: octopus.Round(peak.Value, 3)}
		}
		if includeCost {
			summary["unit_"+costWord] = pounds(total.cost)
			if total.kwh > 0 && total.priced > 0 {
				summary["avg_unit_rate_p_per_kwh"] = octopus.Round(total.cost/total.kwh, 2)
			}
			if total.priced < total.n {
				summary["half_hours_without_a_rate"] = total.n - total.priced
			}
			if !mp.Export {
				sc, days, err := standingCharges(ctx, cl, mp, from, to)
				if err != nil {
					costErrs = append(costErrs, "standing charges: "+err.Error())
				} else if days > 0 {
					summary["standing_charge_gbp"] = pounds(sc)
					summary["standing_charge_days"] = days
					summary["total_cost_gbp"] = pounds(total.cost + sc)
				}
			}
		}

		var tariffs []Obj
		for _, ag := range mp.Agreements {
			if ag.From.Before(to) && (ag.To.IsZero() || ag.To.After(from)) {
				tariffs = append(tariffs, ag.summary())
			}
		}
		out := Obj{
			"account_number": number,
			"meter":          Obj{"fuel": mp.Fuel, "meter_point": mp.ID, "direction": map[bool]string{true: "export", false: "import"}[mp.Export], "serial": serial},
			"from":           octopus.LocalTime(from),
			"to":             octopus.LocalTime(to),
			"group_by":       groupBy,
			"units":          units,
			"summary":        summary,
			"tariffs":        tariffs,
		}
		if mp.Fuel == "gas" {
			delete(out["meter"].(Obj), "direction")
		}
		if rows != nil {
			out["rows"] = rows
		}
		if truncated {
			out["truncated"] = true
			out["note"] = fmt.Sprintf("Only the last %d rows are shown; use a coarser group_by or a shorter period to see everything.", maxRows)
		}
		if len(readings) == 0 {
			out["note"] = "No readings for this period. Smart meter data usually arrives the next day; non-smart meters and meters that don't send half-hourly reads have none."
		}
		if len(assumptions) > 0 {
			out["assumptions"] = unique(assumptions)
		}
		if len(costErrs) > 0 {
			out["errors"] = costErrs
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "consumption", Name: "octopus_get_live_usage", Title: "Live electricity demand",
		Description: "Get near real-time electricity readings from the smart meter through an Octopus Home Mini: current demand in watts and energy used in each interval. " +
			"Needs a Home Mini paired with the smart meter; without one this returns no data. Readings are typically a few seconds to a few minutes old.",
		Enums: map[string][]string{"grouping": {"TEN_SECONDS", "ONE_MINUTE", "FIVE_MINUTES", "HALF_HOURLY", "HOURLY"}},
	}, func(ctx context.Context, a liveArgs) (any, error) {
		number, err := cl.Account(ctx, a.AccountNumber)
		if err != nil {
			return nil, err
		}
		device := a.DeviceID
		if device == "" {
			device, err = importDeviceID(ctx, cl, number)
			if err != nil {
				return nil, err
			}
		}
		minutes := limitArg(a.Minutes, 60, 1440)
		grouping := strings.ToUpper(a.Grouping)
		if grouping == "" {
			grouping = "ONE_MINUTE"
			if minutes > 60 {
				grouping = "FIVE_MINUTES"
			}
		}
		now := cl.Now()
		var res Obj
		err = cl.GraphQL(ctx, `query Telemetry($device: String!, $start: DateTime, $end: DateTime, $grouping: TelemetryGrouping) {
  smartMeterTelemetry(deviceId: $device, start: $start, end: $end, grouping: $grouping) {
    readAt
    demand
    consumptionDelta
    consumption
    export
  }
}`, Obj{"device": device, "start": octopus.FormatTime(now.Add(-time.Duration(minutes) * time.Minute)), "end": octopus.FormatTime(now), "grouping": grouping}, &res)
		if err != nil {
			return nil, err
		}
		list := octopus.List(res, "smartMeterTelemetry")
		sort.SliceStable(list, func(i, j int) bool { return octopus.Str(list[i], "readAt") < octopus.Str(list[j], "readAt") })
		out := Obj{"device_id": device, "grouping": grouping, "minutes": minutes}
		if len(list) == 0 {
			out["note"] = "No live readings. Live data needs an Octopus Home Mini paired with this smart meter, and it must be online."
			return out, nil
		}
		var readings []Obj
		var sumW, maxW, kwh float64
		var nW int
		for _, r := range list {
			row := octopus.Compact(Obj{"read_at": localOrRaw(octopus.Str(r, "readAt")), "demand_w": octopus.NumOrNil(r, 0, "demand")})
			if wh, ok := octopus.Num(r, "consumptionDelta"); ok {
				row["kwh"] = octopus.Round(wh/1000, 4)
				kwh += wh / 1000
			}
			if w, ok := octopus.Num(r, "demand"); ok {
				sumW += w
				nW++
				maxW = math.Max(maxW, w)
			}
			readings = append(readings, row)
		}
		last := list[len(list)-1]
		out["latest"] = octopus.Compact(Obj{
			"read_at":                localOrRaw(octopus.Str(last, "readAt")),
			"demand_w":               octopus.NumOrNil(last, 0, "demand"),
			"meter_import_total_kwh": kwhOrNil(last, "consumption"),
			"meter_export_total_kwh": kwhOrNil(last, "export"),
		})
		if nW > 0 {
			out["average_demand_w"] = octopus.Round(sumW/float64(nW), 0)
			out["max_demand_w"] = octopus.Round(maxW, 0)
		}
		out["kwh_in_window"] = octopus.Round(kwh, 3)
		maxRows := limitArg(a.MaxRows, 60, 1000)
		if len(readings) > maxRows {
			readings = readings[len(readings)-maxRows:]
			out["note"] = fmt.Sprintf("Showing the last %d readings.", maxRows)
		}
		out["readings"] = readings
		return out, nil
	})
}

func kwhOrNil(o Obj, key string) any {
	if wh, ok := octopus.Num(o, key); ok {
		return octopus.Round(wh/1000, 3)
	}
	return nil
}

func localOrRaw(s string) string {
	if t, ok := octopus.ParseTime(s); ok {
		return octopus.LocalTime(t)
	}
	return s
}

// importDeviceID finds the smart meter device ID of the account's electricity import meter.
func importDeviceID(ctx context.Context, cl *octopus.Client, account string) (string, error) {
	var res Obj
	if err := cl.GraphQL(ctx, meterDevicesQuery, Obj{"account": account}, &res); err != nil {
		return "", err
	}
	for _, a := range octopus.List(res, "account", "electricityAgreements") {
		for _, m := range octopus.List(a, "meterPoint", "meters") {
			if id := octopus.Str(m, "smartImportElectricityMeter", "deviceId"); id != "" {
				return id, nil
			}
		}
	}
	return "", errors.New("no smart electricity meter device was found on this account; live readings need a smart meter and an Octopus Home Mini")
}

// standingCharges totals the daily standing charge for each UK day starting in [from, to).
func standingCharges(ctx context.Context, cl *octopus.Client, mp *meterPoint, from, to time.Time) (float64, int, error) {
	cache := map[string]octopus.Rates{}
	total, days := 0.0, 0
	for d := startOfDay(from); d.Before(to); d = time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, octopus.London) {
		if d.Before(from) {
			continue
		}
		if d.After(cl.Now()) {
			break
		}
		ag := mp.agreementAt(d)
		if ag == nil {
			continue
		}
		rates, ok := cache[ag.Tariff.Code]
		if !ok {
			var err error
			rates, err = cl.TariffRates(ctx, ag.Tariff, "standing-charges", maxTime(from, ag.From).Add(-24*time.Hour), to)
			if err != nil {
				return 0, 0, err
			}
			cache[ag.Tariff.Code] = rates
		}
		if r, ok := rates.At(d); ok {
			total += r.IncVAT
			days++
		}
	}
	return total, days, nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func unique(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func merge(maps ...map[string][]string) map[string][]string {
	out := map[string][]string{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}
