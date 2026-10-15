package tools

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
)

// ---------------------------------------------------------------- times

var (
	dateRe      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	localTimeRe = regexp.MustCompile(`(?i)^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(:\d{2})?$`)
)

const timeHelp = "a date (YYYY-MM-DD, a whole day in UK time), a date and time (2026-10-15T17:30, UK time, or RFC 3339 with an offset), or now, today, yesterday or tomorrow"

// parseWhen reads a time argument. Dates are whole days in UK time: as the start of a
// range they mean midnight, and as the end (end=true) they include the whole day.
func parseWhen(s string, end bool, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	day := func(t time.Time) time.Time {
		y, m, d := t.In(octopus.London).Date()
		start := time.Date(y, m, d, 0, 0, 0, 0, octopus.London)
		if end {
			return time.Date(y, m, d+1, 0, 0, 0, 0, octopus.London)
		}
		return start
	}
	switch strings.ToLower(s) {
	case "now":
		return now, nil
	case "today":
		return day(now), nil
	case "yesterday":
		return day(now.AddDate(0, 0, -1)), nil
	case "tomorrow":
		return day(now.AddDate(0, 0, 1)), nil
	}
	switch {
	case dateRe.MatchString(s):
		t, err := time.ParseInLocation("2006-01-02", s, octopus.London)
		if err != nil {
			return time.Time{}, fmt.Errorf("%q is not a valid date", s)
		}
		return day(t), nil
	case localTimeRe.MatchString(s):
		s = strings.Replace(strings.ToUpper(s), " ", "T", 1)
		layout := "2006-01-02T15:04"
		if len(s) == len("2006-01-02T15:04:05") {
			layout = "2006-01-02T15:04:05"
		}
		t, err := time.ParseInLocation(layout, s, octopus.London)
		if err != nil {
			return time.Time{}, fmt.Errorf("%q is not a valid time", s)
		}
		return t, nil
	}
	t, err := time.Parse(time.RFC3339, strings.ToUpper(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("%q isn't a time octopus-mcp understands; use %s", s, timeHelp)
	}
	return t, nil
}

// parseRange reads from/to arguments with defaults, checking the order.
func parseRange(from, to string, defFrom, defTo time.Time, now time.Time) (time.Time, time.Time, error) {
	f, t := defFrom, defTo
	var err error
	if from != "" {
		if f, err = parseWhen(from, false, now); err != nil {
			return f, t, fmt.Errorf("from: %w", err)
		}
	}
	if to != "" {
		if t, err = parseWhen(to, true, now); err != nil {
			return f, t, fmt.Errorf("to: %w", err)
		}
	}
	if !t.After(f) {
		return f, t, fmt.Errorf("to (%s) must be after from (%s)", octopus.LocalTime(t), octopus.LocalTime(f))
	}
	return f, t, nil
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.In(octopus.London).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, octopus.London)
}

// halfHourFloor rounds down to the start of the half-hour.
func halfHourFloor(t time.Time) time.Time { return t.Truncate(30 * time.Minute) }

func pounds(pence float64) float64 { return octopus.Round(pence/100, 2) }

// ---------------------------------------------------------------- accounts and meters

type agreement struct {
	Tariff octopus.Tariff
	From   time.Time
	To     time.Time // zero = open-ended
}

func (a agreement) activeAt(t time.Time) bool {
	return !t.Before(a.From) && (a.To.IsZero() || t.Before(a.To))
}

func (a agreement) summary() Obj {
	o := Obj{
		"tariff_code":  a.Tariff.Code,
		"product_code": a.Tariff.Product,
		"region":       a.Tariff.Region,
		"region_name":  octopus.Regions[a.Tariff.Region],
		"from":         octopus.LocalTime(a.From),
	}
	if a.Tariff.DayNight() {
		o["day_night"] = true
	}
	if !a.To.IsZero() {
		o["to"] = octopus.LocalTime(a.To)
	}
	return o
}

// meterPoint is an electricity (MPAN) or gas (MPRN) supply point from the REST account endpoint.
type meterPoint struct {
	Fuel       string // "electricity" or "gas"
	ID         string // MPAN or MPRN
	Export     bool
	Serials    []string
	Agreements []agreement // oldest first
	Raw        Obj
	PropertyID string
	Address    string
	MovedOut   bool
}

func (mp *meterPoint) agreementAt(t time.Time) *agreement {
	for i := len(mp.Agreements) - 1; i >= 0; i-- {
		if mp.Agreements[i].activeAt(t) {
			return &mp.Agreements[i]
		}
	}
	return nil
}

func (mp *meterPoint) direction() string {
	if mp.Export {
		return "export"
	}
	return "import"
}

func (mp *meterPoint) label() string {
	if mp.Fuel == "gas" {
		return "gas meter point " + mp.ID
	}
	return mp.direction() + " electricity meter point " + mp.ID
}

func propertyAddress(p Obj) string {
	var parts []string
	for _, k := range []string{"address_line_1", "address_line_2", "address_line_3", "town", "county", "postcode"} {
		if s := strings.TrimSpace(octopus.Str(p, k)); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

func parseAgreements(list []Obj) []agreement {
	var out []agreement
	for _, a := range list {
		t, err := octopus.ParseTariff(octopus.Str(a, "tariff_code"))
		if err != nil {
			continue
		}
		from, _ := octopus.ParseTime(octopus.Str(a, "valid_from"))
		to, _ := octopus.ParseTime(octopus.Str(a, "valid_to"))
		out = append(out, agreement{Tariff: t, From: from, To: to})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].From.Before(out[j].From) })
	return out
}

// loadAccount fetches the account from the REST API and flattens its meter points.
func loadAccount(ctx context.Context, cl *octopus.Client, given string) (string, Obj, []*meterPoint, error) {
	number, err := cl.Account(ctx, given)
	if err != nil {
		return "", nil, nil, err
	}
	var acct Obj
	if err := cl.Get(ctx, "/accounts/"+url.PathEscape(number)+"/", nil, &acct); err != nil {
		return number, nil, nil, err
	}
	var mps []*meterPoint
	for _, p := range octopus.List(acct, "properties") {
		movedOut := octopus.Str(p, "moved_out_at") != ""
		pid, addr := octopus.Str(p, "id"), propertyAddress(p)
		for _, e := range octopus.List(p, "electricity_meter_points") {
			mp := &meterPoint{Fuel: "electricity", ID: octopus.Str(e, "mpan"), Export: octopus.Bool(e, "is_export"), Raw: e, PropertyID: pid, Address: addr, MovedOut: movedOut}
			for _, m := range octopus.List(e, "meters") {
				if s := octopus.Str(m, "serial_number"); s != "" {
					mp.Serials = append(mp.Serials, s)
				}
			}
			mp.Agreements = parseAgreements(octopus.List(e, "agreements"))
			mps = append(mps, mp)
		}
		for _, g := range octopus.List(p, "gas_meter_points") {
			mp := &meterPoint{Fuel: "gas", ID: octopus.Str(g, "mprn"), Raw: g, PropertyID: pid, Address: addr, MovedOut: movedOut}
			for _, m := range octopus.List(g, "meters") {
				if s := octopus.Str(m, "serial_number"); s != "" {
					mp.Serials = append(mp.Serials, s)
				}
			}
			mp.Agreements = parseAgreements(octopus.List(g, "agreements"))
			mps = append(mps, mp)
		}
	}
	return number, acct, mps, nil
}

// MeterArgs pick a meter point on the account.
type MeterArgs struct {
	AccountNumber string `json:"account_number,omitempty" jsonschema:"Account number like A-1234ABCD (default OCTOPUS_ACCOUNT, or the only account the API key can see)"`
	Fuel          string `json:"fuel,omitempty" jsonschema:"electricity (default) or gas"`
	Direction     string `json:"direction,omitempty" jsonschema:"For electricity: import (default, what you use) or export (what you send to the grid, e.g. from solar panels)"`
	MeterPoint    string `json:"meter_point,omitempty" jsonschema:"A specific MPAN (electricity) or MPRN (gas), when the account has several (see octopus_get_account)"`
	Serial        string `json:"serial,omitempty" jsonschema:"A specific meter serial number (default: the meter point's meter that has readings)"`
}

var meterEnums = map[string][]string{"fuel": {"electricity", "gas"}, "direction": {"import", "export"}}

// pickMeterPoint chooses the meter point the arguments describe, preferring current properties.
func pickMeterPoint(mps []*meterPoint, a MeterArgs) (*meterPoint, error) {
	fuel := strings.ToLower(a.Fuel)
	if fuel == "" {
		fuel = "electricity"
	}
	wantExport := strings.EqualFold(a.Direction, "export")
	var candidates []*meterPoint
	for _, mp := range mps {
		switch {
		case a.MeterPoint != "":
			if mp.ID == strings.TrimSpace(a.MeterPoint) {
				return mp, nil
			}
		case mp.Fuel == fuel && (fuel == "gas" || mp.Export == wantExport):
			candidates = append(candidates, mp)
		}
	}
	if a.MeterPoint != "" {
		return nil, fmt.Errorf("meter point %s isn't on this account; see octopus_get_account", a.MeterPoint)
	}
	sort.SliceStable(candidates, func(i, j int) bool { return !candidates[i].MovedOut && candidates[j].MovedOut })
	if len(candidates) == 0 {
		what := fuel
		if fuel == "electricity" {
			what = map[bool]string{true: "export", false: "import"}[wantExport] + " electricity"
		}
		return nil, fmt.Errorf("the account has no %s meter point", what)
	}
	if len(candidates) > 1 && !candidates[1].MovedOut {
		var ids []string
		for _, c := range candidates {
			if !c.MovedOut {
				ids = append(ids, c.ID)
			}
		}
		return nil, fmt.Errorf("the account has several matching meter points (%s); pass meter_point", strings.Join(ids, ", "))
	}
	return candidates[0], nil
}

// ---------------------------------------------------------------- GraphQL device IDs

// smartDevices maps meter serial numbers to smart meter details from GraphQL:
// the device ID used for Home Mini telemetry, and the units a gas meter reports in.
type smartDevice struct {
	DeviceID       string
	ExportDeviceID string
	Units          string
	MeterType      string
	TariffName     string
}

const meterDevicesQuery = `query AccountMeters($account: String!) {
  account(accountNumber: $account) {
    electricityAgreements(active: true) {
      tariff { ... on TariffType { displayName tariffCode } }
      meterPoint {
        mpan
        meters(includeInactive: false) {
          serialNumber
          meterType
          smartImportElectricityMeter { deviceId }
          smartExportElectricityMeter { deviceId }
        }
      }
    }
    gasAgreements(active: true) {
      tariff { displayName tariffCode }
      meterPoint {
        mprn
        meters(includeInactive: false) {
          serialNumber
          consumptionUnits
          smartGasMeter { deviceId }
        }
      }
    }
  }
}`

func loadSmartDevices(ctx context.Context, cl *octopus.Client, account string) (map[string]smartDevice, error) {
	var res Obj
	if err := cl.GraphQL(ctx, meterDevicesQuery, Obj{"account": account}, &res); err != nil {
		return nil, err
	}
	out := map[string]smartDevice{}
	for _, a := range octopus.List(res, "account", "electricityAgreements") {
		for _, m := range octopus.List(a, "meterPoint", "meters") {
			out[octopus.Str(m, "serialNumber")] = smartDevice{
				DeviceID:       octopus.Str(m, "smartImportElectricityMeter", "deviceId"),
				ExportDeviceID: octopus.Str(m, "smartExportElectricityMeter", "deviceId"),
				MeterType:      octopus.Str(m, "meterType"),
				TariffName:     octopus.Str(a, "tariff", "displayName"),
			}
		}
	}
	for _, a := range octopus.List(res, "account", "gasAgreements") {
		for _, m := range octopus.List(a, "meterPoint", "meters") {
			out[octopus.Str(m, "serialNumber")] = smartDevice{
				DeviceID:   octopus.Str(m, "smartGasMeter", "deviceId"),
				Units:      octopus.Str(m, "consumptionUnits"),
				TariffName: octopus.Str(a, "tariff", "displayName"),
			}
		}
	}
	return out, nil
}

type errorList []string

func (e errorList) Error() string { return strings.Join(e, "\n") }
