package octopus

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // UK local time must work even on systems without a zoneinfo database
)

// London is UK local time, which Octopus uses for days, weeks and months.
var London = func() *time.Location {
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		panic(err)
	}
	return loc
}()

// Regions are the 14 electricity distribution regions (GSP groups) that tariff codes end with.
var Regions = map[string]string{
	"A": "Eastern England",
	"B": "East Midlands",
	"C": "London",
	"D": "Merseyside and North Wales",
	"E": "West Midlands",
	"F": "North Eastern England",
	"G": "North Western England",
	"H": "Southern England",
	"J": "South Eastern England",
	"K": "South Wales",
	"L": "South Western England",
	"M": "Yorkshire",
	"N": "Southern Scotland",
	"P": "Northern Scotland",
}

// RegionLetter normalises "_C", "c" or "C" to "C", or returns "" if it isn't a region.
func RegionLetter(s string) string {
	s = strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(s), "_"))
	if _, ok := Regions[s]; ok {
		return s
	}
	return ""
}

// Tariff is a parsed tariff code such as E-1R-AGILE-24-10-01-C.
type Tariff struct {
	Code      string
	Fuel      string // "electricity" or "gas"
	Registers string // "1R" single rate, "2R" day/night (Economy 7), ...
	Product   string // e.g. AGILE-24-10-01
	Region    string // e.g. C
}

// ParseTariff splits a tariff code into its parts.
func ParseTariff(code string) (Tariff, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	parts := strings.Split(code, "-")
	if len(parts) < 4 || (parts[0] != "E" && parts[0] != "G") || !strings.HasSuffix(parts[1], "R") || RegionLetter(parts[len(parts)-1]) == "" {
		return Tariff{}, fmt.Errorf("%q is not a tariff code; they look like E-1R-AGILE-24-10-01-C (fuel, registers, product code, region)", code)
	}
	t := Tariff{Code: code, Registers: parts[1], Product: strings.Join(parts[2:len(parts)-1], "-"), Region: parts[len(parts)-1], Fuel: "electricity"}
	if parts[0] == "G" {
		t.Fuel = "gas"
	}
	return t, nil
}

// TariffCode builds a tariff code from a product code and region.
func TariffCode(fuel, registers, product, region string) string {
	prefix := "E"
	if fuel == "gas" {
		prefix = "G"
	}
	if registers == "" {
		registers = "1R"
	}
	return fmt.Sprintf("%s-%s-%s-%s", prefix, strings.ToUpper(registers), strings.ToUpper(product), RegionLetter(region))
}

// DayNight reports whether the tariff has separate day and night rates (Economy 7).
func (t Tariff) DayNight() bool { return t.Registers == "2R" }

// Path is the REST path of one of the tariff's charge lists, e.g. "standard-unit-rates".
func (t Tariff) Path(kind string) string {
	return fmt.Sprintf("/products/%s/%s-tariffs/%s/%s/", url.PathEscape(t.Product), t.Fuel, url.PathEscape(t.Code), kind)
}

// Rate is a unit rate (pence per kWh) or standing charge (pence per day) over a period.
type Rate struct {
	From   time.Time
	To     time.Time // zero means open-ended
	IncVAT float64
	ExcVAT float64
}

// Contains reports whether t falls in the rate's period.
func (r Rate) Contains(t time.Time) bool {
	return !t.Before(r.From) && (r.To.IsZero() || t.Before(r.To))
}

// Rates is a series sorted by start time.
type Rates []Rate

// At returns the rate in force at t.
func (rs Rates) At(t time.Time) (Rate, bool) {
	i := sort.Search(len(rs), func(i int) bool { return rs[i].From.After(t) })
	for j := i - 1; j >= 0 && j >= i-3; j-- { // overlapping records are rare; look back a little
		if rs[j].Contains(t) {
			return rs[j], true
		}
	}
	return Rate{}, false
}

// ParseTime reads an API timestamp.
func ParseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

// FormatTime formats a time for the API: UTC with a Z suffix, as Octopus recommends.
func FormatTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// LocalTime formats a time in UK local time for output.
func LocalTime(t time.Time) string { return t.In(London).Format(time.RFC3339) }

// maxRateRecords bounds how much rate history one call fetches (about 13 months of half-hours).
const maxRateRecords = 20000

// TariffRates fetches a tariff's unit rates ("standard-unit-rates", "day-unit-rates",
// "night-unit-rates") or "standing-charges" overlapping [from, to). These endpoints are public.
// Where Octopus publishes separate direct debit and non-direct-debit prices, the direct debit ones are kept.
func (c *Client) TariffRates(ctx context.Context, t Tariff, kind string, from, to time.Time) (Rates, error) {
	q := url.Values{"page_size": {"1500"}}
	if !from.IsZero() {
		q.Set("period_from", FormatTime(from))
	}
	if !to.IsZero() {
		q.Set("period_to", FormatTime(to))
	}
	page, err := c.GetPages(ctx, t.Path(kind), q, false, maxRateRecords)
	if err != nil {
		return nil, err
	}
	hasDD := false
	for _, r := range page.Results {
		if Str(r, "payment_method") == "DIRECT_DEBIT" {
			hasDD = true
		}
	}
	var out Rates
	for _, r := range page.Results {
		if pm := Str(r, "payment_method"); hasDD && pm != "" && pm != "DIRECT_DEBIT" {
			continue
		}
		rf, ok := ParseTime(Str(r, "valid_from"))
		if !ok {
			rf = time.Unix(0, 0)
		}
		rt, _ := ParseTime(Str(r, "valid_to"))
		inc, _ := Num(r, "value_inc_vat")
		exc, _ := Num(r, "value_exc_vat")
		out = append(out, Rate{From: rf, To: rt, IncVAT: inc, ExcVAT: exc})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].From.Before(out[j].From) })
	return out, nil
}

// UnitRateLookup finds the unit rate for a moment on a tariff.
type UnitRateLookup struct {
	Tariff   Tariff
	Standard Rates
	Day      Rates
	Night    Rates
}

// NightStartUTC and NightEndUTC are the Economy 7 night hours assumed for day/night tariffs.
// Real meters vary by region and meter (often 7 hours between midnight and 08:30 GMT); Octopus's
// API doesn't say which hours a meter uses.
const (
	NightStartUTC = 30  // minutes after midnight UTC (00:30)
	NightEndUTC   = 450 // 07:30 UTC
)

// IsNight reports whether t falls in the assumed Economy 7 night period.
func IsNight(t time.Time) bool {
	u := t.UTC()
	m := u.Hour()*60 + u.Minute()
	return m >= NightStartUTC && m < NightEndUTC
}

// LoadUnitRates fetches everything needed to price consumption on a tariff over [from, to).
func (c *Client) LoadUnitRates(ctx context.Context, t Tariff, from, to time.Time) (*UnitRateLookup, error) {
	l := &UnitRateLookup{Tariff: t}
	var err error
	if t.DayNight() {
		if l.Day, err = c.TariffRates(ctx, t, "day-unit-rates", from, to); err != nil {
			return nil, err
		}
		if l.Night, err = c.TariffRates(ctx, t, "night-unit-rates", from, to); err != nil {
			return nil, err
		}
		return l, nil
	}
	l.Standard, err = c.TariffRates(ctx, t, "standard-unit-rates", from, to)
	return l, err
}

// At returns the unit rate in force at t.
func (l *UnitRateLookup) At(t time.Time) (Rate, bool) {
	if l.Tariff.DayNight() {
		if IsNight(t) {
			return l.Night.At(t)
		}
		return l.Day.At(t)
	}
	return l.Standard.At(t)
}
