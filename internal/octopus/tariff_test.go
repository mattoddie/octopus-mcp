package octopus_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
	"github.com/mattoddie/octopus-mcp/internal/octopus/octopustest"
)

func TestRatesAtWithOverlappingRecords(t *testing.T) {
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	// An open-ended rate from day 0, then a run of half-hourly records from day 1:
	// the open-ended one is many records back by the time those end.
	rs := octopus.Rates{{From: base, IncVAT: 24}}
	for i := 0; i < 10; i++ {
		s := base.Add(24*time.Hour + time.Duration(i)*30*time.Minute)
		rs = append(rs, octopus.Rate{From: s, To: s.Add(30 * time.Minute), IncVAT: float64(i)})
	}
	for at, want := range map[time.Time]float64{
		base.Add(time.Hour):                     24,
		base.Add(24*time.Hour + 75*time.Minute): 2,
		base.Add(24*time.Hour + 5*time.Hour):    24, // after the half-hourly run, the open-ended rate applies again
		base.Add(30 * 24 * time.Hour):           24,
	} {
		if r, ok := rs.At(at); !ok || r.IncVAT != want {
			t.Errorf("%s: %v %v, want %v", at, r.IncVAT, ok, want)
		}
	}
	if _, ok := rs.At(base.Add(-time.Minute)); ok {
		t.Error("rate before the first record")
	}
	// A gap with nothing in force.
	gap := octopus.Rates{{From: base, To: base.Add(time.Hour), IncVAT: 1}, {From: base.Add(2 * time.Hour), To: base.Add(3 * time.Hour), IncVAT: 2}}
	if _, ok := gap.At(base.Add(90 * time.Minute)); ok {
		t.Error("rate in a gap")
	}
}

func TestTariffRatesRefusesTruncatedHistory(t *testing.T) {
	m, c := newClient(t)
	base := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	m.On("GET", "/products/AGILE-24-10-01/electricity-tariffs/E-1R-AGILE-24-10-01-C/standard-unit-rates/", func(r octopustest.Request, _ []string) (int, any) {
		size := 1500
		var results []any
		for i := 0; i < size; i++ {
			s := base.Add(time.Duration(i) * 30 * time.Minute)
			results = append(results, map[string]any{"value_inc_vat": 10.0, "value_exc_vat": 9.5, "valid_from": s.Format(time.RFC3339), "valid_to": s.Add(30 * time.Minute).Format(time.RFC3339)})
		}
		return 200, map[string]any{"count": 99999, "next": "https://api.octopus.energy/v1/products/AGILE-24-10-01/electricity-tariffs/E-1R-AGILE-24-10-01-C/standard-unit-rates/?page=2", "results": results}
	})
	tr, _ := octopus.ParseTariff("E-1R-AGILE-24-10-01-C")
	_, err := c.TariffRates(context.Background(), tr, "standard-unit-rates", base, base.Add(24*time.Hour))
	if err == nil || !strings.Contains(err.Error(), "shorter period") {
		t.Errorf("truncated: %v", err)
	}
}

func TestTariffRatesFetchesTwoYears(t *testing.T) {
	m, c := newClient(t)
	base := time.Date(2024, 10, 15, 0, 0, 0, 0, time.UTC)
	const total = 732 * 48
	m.On("GET", "/products/AGILE-24-10-01/electricity-tariffs/E-1R-AGILE-24-10-01-C/standard-unit-rates/", func(r octopustest.Request, _ []string) (int, any) {
		page := 1
		if p, err := strconv.Atoi(r.Query.Get("page")); err == nil {
			page = p
		}
		var results []any
		for i := (page - 1) * 1500; i < min(page*1500, total); i++ {
			s := base.Add(time.Duration(i) * 30 * time.Minute)
			results = append(results, map[string]any{"value_inc_vat": 10.0, "value_exc_vat": 9.5, "valid_from": s.Format(time.RFC3339), "valid_to": s.Add(30 * time.Minute).Format(time.RFC3339)})
		}
		out := map[string]any{"count": total, "next": nil, "results": results}
		if page*1500 < total {
			out["next"] = "https://api.octopus.energy/v1/products/AGILE-24-10-01/electricity-tariffs/E-1R-AGILE-24-10-01-C/standard-unit-rates/?page=" + strconv.Itoa(page+1)
		}
		return 200, out
	})
	tr, _ := octopus.ParseTariff("E-1R-AGILE-24-10-01-C")
	rs, err := c.TariffRates(context.Background(), tr, "standard-unit-rates", base, base.Add(732*24*time.Hour))
	if err != nil || len(rs) != total {
		t.Fatalf("two years: %d records, %v", len(rs), err)
	}
}
