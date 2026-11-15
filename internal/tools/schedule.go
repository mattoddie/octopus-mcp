package tools

// octopus_plan_schedule, in the tariffs toolset: spreads a battery, heat pump
// or car's energy over the cheapest half-hours of a tariff's known windows.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
)

type scheduleArgs struct {
	TariffArgs
	EnergyKWh *float64 `json:"energy_kwh" jsonschema:"Energy to put in over the period, in kWh: for example 9 to charge a 10 kWh battery from 10% to full, or a heat pump's expected electricity use for the day"`
	PowerKW   *float64 `json:"power_kw" jsonschema:"The most the device draws, in kW: the battery's charge rate, the heat pump's input power or the car charger's rating (7 for most home chargers)"`
	From      string   `json:"from,omitempty" jsonschema:"Earliest start: a date and time, or now (default now)"`
	To        string   `json:"to,omitempty" jsonschema:"When it must be done by: a date and time, or a date (inclusive; default as far ahead as prices are published)"`
}

// maxScheduleWindows bounds the tariff price bands listed.
const maxScheduleWindows = 48

func registerSchedule(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "tariffs", Name: "octopus_plan_schedule", Title: "Plan a charging or heating schedule",
		Description: "Plan when to charge a home battery or car, or run a heat pump, so that a given amount of energy goes in as cheaply as possible on a time-of-use tariff such as Cosy, Flux, Go, Intelligent Go or Agile. Unlike octopus_find_cheapest_slots, which finds one continuous block, this spreads the energy over the cheapest half-hours the device's power allows, across every cheap window in the period, and merges them into start and stop times. " +
			"Also lists the tariff's price windows over the period (for example Cosy's three cheap windows and its peak) and compares the cost with starting straight away. It only plans; it doesn't change any device. Only uses published prices. Choose the tariff by tariff_code, by product_code with region or postcode, or leave them out for the account's current tariff.",
		Enums: tariffEnums,
	}, func(ctx context.Context, a scheduleArgs) (any, error) {
		if a.EnergyKWh == nil || *a.EnergyKWh <= 0 {
			return nil, errors.New("energy_kwh must be more than 0")
		}
		if a.PowerKW == nil || *a.PowerKW <= 0 || *a.PowerKW > 100 {
			return nil, errors.New("power_kw must be more than 0 and at most 100")
		}
		energy, power := *a.EnergyKWh, *a.PowerKW
		now := cl.Now()
		from, to, err := parseRange(a.From, a.To, now, startOfDay(now).AddDate(0, 0, 2), now)
		if err != nil {
			return nil, err
		}
		if to.Sub(from) > 7*24*time.Hour {
			return nil, errors.New("plan at most a week ahead")
		}
		t, source, err := resolveTariff(ctx, cl, a.TariffArgs)
		if err != nil {
			return nil, err
		}
		if t.Fuel != "electricity" {
			return nil, fmt.Errorf("%s is a gas tariff; schedules are for electricity", t.Code)
		}
		if t.DayNight() {
			return nil, fmt.Errorf("%s is a day/night tariff: run things in its night period (Economy 7 hours vary by meter)", t.Code)
		}
		rs, err := cl.TariffRates(ctx, t, "standard-unit-rates", from.Add(-24*time.Hour), to)
		if err != nil {
			return nil, err
		}
		// Whole half-hours inside [from, to), up to where prices are known.
		var slots []octopus.Rate
		for s := halfHourFloor(from); !s.Add(30 * time.Minute).After(to); s = s.Add(30 * time.Minute) {
			if s.Before(from) {
				continue
			}
			r, ok := rs.At(s)
			if !ok {
				break
			}
			slots = append(slots, octopus.Rate{From: s, To: s.Add(30 * time.Minute), IncVAT: r.IncVAT})
		}
		perSlot := power / 2 // kWh in a half-hour at full power
		if len(slots) == 0 {
			return nil, fmt.Errorf("no prices are published between %s and %s", octopus.LocalTime(from), octopus.LocalTime(to))
		}
		if most := perSlot * float64(len(slots)); energy > most+1e-9 {
			return nil, fmt.Errorf("at %g kW, at most %.1f kWh fits in the %d published half-hours between %s and %s; lower energy_kwh or allow more time", power, most, len(slots), octopus.LocalTime(slots[0].From), octopus.LocalTime(slots[len(slots)-1].To))
		}

		// The cheapest half-hours get full power; the remainder goes in the
		// dearest of the chosen ones, which is the cheapest way to split it.
		need := int(math.Ceil(energy/perSlot - 1e-9))
		order := make([]int, len(slots))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(x, y int) bool { return slots[order[x]].IncVAT < slots[order[y]].IncVAT })
		chosen := order[:need]
		kwh := make([]float64, len(slots))
		for _, i := range chosen {
			kwh[i] = perSlot
		}
		if rest := energy - perSlot*float64(need-1); rest < perSlot {
			kwh[chosen[need-1]] = rest
		}

		// Merge consecutive half-hours into start and stop times.
		var windows []Obj
		var cost float64
		for i := 0; i < len(slots); {
			if kwh[i] == 0 {
				i++
				continue
			}
			j := i
			var wk, wc float64
			for ; j < len(slots) && kwh[j] > 0; j++ {
				wk += kwh[j]
				wc += kwh[j] * slots[j].IncVAT
			}
			w := Obj{"start": octopus.LocalTime(slots[i].From), "end": octopus.LocalTime(slots[j-1].To), "kwh": octopus.Round(wk, 2), "average_p_per_kwh": octopus.Round(wc/wk, 2), "cost_gbp": pounds(wc)}
			if wk < perSlot*float64(j-i)-1e-9 {
				w["note"] = "Part of this window is enough: the device doesn't need full power for all of it."
			}
			windows = append(windows, w)
			cost += wc
			i = j
		}

		// The cost of running at full power from the first half-hour instead.
		var asap float64
		left := energy
		for _, s := range slots {
			e := math.Min(perSlot, left)
			asap += e * s.IncVAT
			if left -= e; left <= 1e-9 {
				break
			}
		}

		// The tariff's price bands, so the model can see the cheap and peak windows.
		var bands []Obj
		for i := 0; i < len(slots) && len(bands) < maxScheduleWindows; {
			j := i
			for j < len(slots) && slots[j].IncVAT == slots[i].IncVAT {
				j++
			}
			bands = append(bands, Obj{"start": octopus.LocalTime(slots[i].From), "end": octopus.LocalTime(slots[j-1].To), "p_per_kwh": octopus.Round(slots[i].IncVAT, 2)})
			i = j
		}

		out := Obj{
			"tariff":                  tariffInfo(t, source),
			"energy_kwh":              octopus.Round(energy, 2),
			"power_kw":                power,
			"searched_from":           octopus.LocalTime(slots[0].From),
			"prices_known_until":      octopus.LocalTime(slots[len(slots)-1].To),
			"schedule":                windows,
			"hours_running":           float64(need) / 2,
			"total_cost_gbp":          pounds(cost),
			"average_p_per_kwh":       octopus.Round(cost/energy, 2),
			"cost_if_started_now_gbp": pounds(asap),
			"saving_gbp":              pounds(asap - cost),
			"tariff_windows":          bands,
		}
		if len(bands) == maxScheduleWindows {
			out["tariff_windows_truncated"] = true
		}
		var notes []string
		if bands := len(bands); bands == 1 {
			notes = append(notes, "The price is the same throughout this window, so timing makes no difference on this tariff.")
		}
		notes = append(notes, "Assumes the device can start and stop on the half-hour and runs at power_kw when on. Heat pumps and batteries lose some energy, so allow a margin; a heat pump heating the house ahead of time works best just before the heat is needed.")
		out["notes"] = notes
		return out, nil
	})
}
