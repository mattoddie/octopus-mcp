package tools

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

type deviceArgs struct {
	AccountNumber string `json:"account_number,omitempty" jsonschema:"Account number like A-1234ABCD (default OCTOPUS_ACCOUNT, or the only account the API key can see)"`
	DeviceID      string `json:"device_id,omitempty" jsonschema:"The device's ID from octopus_list_devices (default: the account's only car or charger)"`
}

type dispatchArgs struct {
	deviceArgs
	CompletedHours *int `json:"completed_hours,omitempty" jsonschema:"How many hours of completed dispatches to include (default 24, at most 168). Octopus reports them about 12 hours late"`
}

type preferencesArgs struct {
	deviceArgs
	TargetPercent *int     `json:"target_percent,omitempty" jsonschema:"Charge the battery to this percentage by the ready-by time (10 to 100)"`
	ReadyBy       string   `json:"ready_by,omitempty" jsonschema:"Time the car must be ready, as HH:MM on the hour or half-hour (the Octopus app offers 04:00 to 11:00)"`
	Days          []string `json:"days,omitempty" jsonschema:"Days of the week to change (default every day)"`
}

type boostArgs struct {
	deviceArgs
	Action string `json:"action,omitempty" jsonschema:"BOOST (default) to start charging now, or CANCEL to stop a boost"`
}

type smartControlArgs struct {
	deviceArgs
	Enabled bool `json:"enabled" jsonschema:"true to let Octopus schedule charging again; false to suspend smart charging so the device charges on its own settings"`
}

var weekdays = []string{"MONDAY", "TUESDAY", "WEDNESDAY", "THURSDAY", "FRIDAY", "SATURDAY", "SUNDAY"}

const devicesQuery = `query Devices($account: String!) {
  devices(accountNumber: $account) {
    id
    name
    deviceType
    propertyId
    status {
      current
      currentState
      isSuspended
      ... on SmartFlexVehicleStatus { stateOfCharge { value timestamp } }
      ... on SmartFlexChargePointStatus { stateOfCharge { value timestamp } }
    }
    preferences {
      mode
      unit
      targetType
      schedules { dayOfWeek time min max }
    }
    ... on SmartFlexVehicle { make model vehicleBatterySize chargePointPowerOutput }
    ... on SmartFlexChargePoint { make model vehicleBatterySize chargePointPowerOutput }
  }
}`

func loadDevices(ctx context.Context, cl *octopus.Client, account string) ([]Obj, error) {
	var res Obj
	if err := cl.GraphQL(ctx, devicesQuery, Obj{"account": account}, &res); err != nil {
		return nil, err
	}
	return octopus.List(res, "devices"), nil
}

func summarizeDevice(d Obj) Obj {
	out := octopus.Compact(Obj{
		"id":                 octopus.Str(d, "id"),
		"name":               octopus.Str(d, "name"),
		"type":               octopus.Str(d, "deviceType"),
		"make":               octopus.Str(d, "make"),
		"model":              octopus.Str(d, "model"),
		"battery_kwh":        octopus.NumOrNil(d, 1, "vehicleBatterySize"),
		"charger_kw":         octopus.NumOrNil(d, 1, "chargePointPowerOutput"),
		"lifecycle":          octopus.Str(d, "status", "current"),
		"state":              octopus.Str(d, "status", "currentState"),
		"smart_control_off":  octopus.Bool(d, "status", "isSuspended"),
		"state_of_charge":    octopus.NumOrNil(d, 0, "status", "stateOfCharge", "value"),
		"state_of_charge_at": octopus.Str(d, "status", "stateOfCharge", "timestamp"),
	})
	if p, ok := d["preferences"].(Obj); ok {
		var sched []Obj
		for _, s := range octopus.List(p, "schedules") {
			sched = append(sched, octopus.Compact(Obj{"day": octopus.Str(s, "dayOfWeek"), "ready_by": shortTime(octopus.Str(s, "time")), "target": octopus.NumOrNil(s, 0, "max"), "minimum": octopus.NumOrNil(s, 0, "min")}))
		}
		out["preferences"] = octopus.Compact(Obj{"mode": octopus.Str(p, "mode"), "unit": octopus.Str(p, "unit"), "target_type": octopus.Str(p, "targetType"), "schedules": sched})
	}
	return out
}

func shortTime(s string) string {
	if len(s) >= 5 {
		return s[:5]
	}
	return s
}

// pickDevice chooses the device to act on: the given ID, or the account's only car or charger.
func pickDevice(ctx context.Context, cl *octopus.Client, a deviceArgs) (string, Obj, error) {
	number, err := cl.Account(ctx, a.AccountNumber)
	if err != nil {
		return "", nil, err
	}
	devices, err := loadDevices(ctx, cl, number)
	if err != nil {
		return number, nil, err
	}
	if a.DeviceID != "" {
		for _, d := range devices {
			if octopus.Str(d, "id") == a.DeviceID {
				return number, d, nil
			}
		}
		return number, nil, fmt.Errorf("device %s isn't on account %s; see octopus_list_devices", a.DeviceID, number)
	}
	var evs []Obj
	for _, d := range devices {
		if octopus.Str(d, "deviceType") == "ELECTRIC_VEHICLES" && octopus.Str(d, "status", "current") != "RETIRED" {
			evs = append(evs, d)
		}
	}
	switch len(evs) {
	case 0:
		return number, nil, errors.New("no car or charger is connected to Intelligent Octopus on this account; see octopus_list_devices")
	case 1:
		return number, evs[0], nil
	}
	var ids []string
	for _, d := range evs {
		ids = append(ids, octopus.Str(d, "id"))
	}
	return number, nil, fmt.Errorf("the account has several cars or chargers (%s); pass device_id", strings.Join(ids, ", "))
}

var hhmmRe = regexp.MustCompile(`^([01]\d|2[0-3]):(00|30)$`)

func registerIntelligent(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "intelligent", Name: "octopus_list_devices", Title: "List smart devices",
		Description: "List the devices Octopus controls for smart tariffs such as Intelligent Octopus Go: electric cars, chargers, batteries and heat pumps, with each one's make and model, status (for example SMART_CONTROL_CAPABLE, BOOSTING or LOST_CONNECTION), whether smart charging is suspended, battery level where reported, and its charging schedule (target charge and ready-by time for each day).",
	}, func(ctx context.Context, a deviceArgs) (any, error) {
		number, err := cl.Account(ctx, a.AccountNumber)
		if err != nil {
			return nil, err
		}
		devices, err := loadDevices(ctx, cl, number)
		if err != nil {
			return nil, err
		}
		items := []Obj{}
		for _, d := range devices {
			if a.DeviceID == "" || octopus.Str(d, "id") == a.DeviceID {
				items = append(items, summarizeDevice(d))
			}
		}
		out := Obj{"account_number": number, "devices": items}
		if len(items) == 0 {
			out["note"] = "No smart devices are registered. They appear once a car or charger is connected to Intelligent Octopus in the Octopus app."
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "intelligent", Name: "octopus_get_dispatches", Title: "Intelligent Octopus dispatches",
		Description: "Get Intelligent Octopus charging slots (dispatches) for a car or charger: the planned slots Octopus has scheduled (with the energy each should add) and recent completed slots. " +
			"On Intelligent Octopus Go, home electricity is charged at the off-peak rate during planned smart-charging slots even outside the usual off-peak hours (Octopus caps how many hours a day this applies to; check your tariff terms). Completed slots are reported about 12 hours late.",
	}, func(ctx context.Context, a dispatchArgs) (any, error) {
		number, device, err := pickDevice(ctx, cl, a.deviceArgs)
		if err != nil {
			return nil, err
		}
		id := octopus.Str(device, "id")
		var res Obj
		err = cl.GraphQL(ctx, `query Dispatches($account: String!, $device: String!) {
  flexPlannedDispatches(deviceId: $device) { start end type energyAddedKwh }
  completedDispatches(accountNumber: $account) { start end delta meta { source location } }
}`, Obj{"account": number, "device": id}, &res)
		var gerr octopus.GraphQLErrors
		var errs []string
		if err != nil {
			// Planned and completed dispatches fail independently; keep what came back.
			if !errors.As(err, &gerr) || res == nil {
				return nil, err
			}
			errs = append(errs, err.Error())
		}
		now := cl.Now()
		var planned []Obj
		var current Obj
		plannedKWh := 0.0
		for _, d := range octopus.List(res, "flexPlannedDispatches") {
			s, _ := octopus.ParseTime(octopus.Str(d, "start"))
			e, _ := octopus.ParseTime(octopus.Str(d, "end"))
			row := octopus.Compact(Obj{"start": octopus.LocalTime(s), "end": octopus.LocalTime(e), "type": octopus.Str(d, "type"), "energy_kwh": octopus.NumOrNil(d, 2, "energyAddedKwh")})
			if kwh, ok := octopus.Num(d, "energyAddedKwh"); ok {
				plannedKWh += kwh
			}
			if !now.Before(s) && now.Before(e) {
				current = row
			}
			planned = append(planned, row)
		}
		hours := limitArg(a.CompletedHours, 24, 168)
		since := now.Add(-time.Duration(hours) * time.Hour)
		var completed []Obj
		for _, d := range octopus.List(res, "completedDispatches") {
			s, _ := octopus.ParseTime(octopus.Str(d, "start"))
			e, _ := octopus.ParseTime(octopus.Str(d, "end"))
			if e.Before(since) {
				continue
			}
			completed = append(completed, octopus.Compact(Obj{"start": octopus.LocalTime(s), "end": octopus.LocalTime(e), "energy_kwh": octopus.NumOrNil(d, 2, "delta"), "source": octopus.Str(d, "meta", "source"), "location": octopus.Str(d, "meta", "location")}))
		}
		sort.SliceStable(completed, func(i, j int) bool { return completed[i]["start"].(string) < completed[j]["start"].(string) })
		if planned == nil {
			planned = []Obj{}
		}
		if completed == nil {
			completed = []Obj{}
		}
		out := Obj{
			"device":            summarizeDevice(device),
			"planned":           planned,
			"planned_total_kwh": octopus.Round(plannedKWh, 2),
			"completed":         completed,
			"in_dispatch_now":   current != nil,
		}
		if current != nil {
			out["current_dispatch"] = current
		}
		if len(planned) == 0 {
			out["note"] = "No charging is planned. Octopus plans slots once the car is plugged in and its target and ready-by time are set."
		}
		if len(errs) > 0 {
			out["errors"] = errs
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "intelligent", Name: "octopus_set_charge_preferences", Title: "Set charging target and ready-by time",
		Description: "Change an Intelligent Octopus car's charging schedule: the battery percentage to reach and the time it must be ready by, for every day or chosen days. Days you don't name keep their current settings. " +
			"Octopus plans the next charge from these settings; the Octopus app offers ready-by times from 04:00 to 11:00 and targets in steps, and Octopus may reject other values. Confirm the change with the user first.",
		Gate: Write, Idempotent: true,
		Enums: map[string][]string{"days": weekdays},
	}, func(ctx context.Context, a preferencesArgs) (any, error) {
		if a.TargetPercent == nil && a.ReadyBy == "" {
			return nil, errors.New("give target_percent, ready_by or both")
		}
		if a.TargetPercent != nil && (*a.TargetPercent < 10 || *a.TargetPercent > 100) {
			return nil, errors.New("target_percent must be between 10 and 100")
		}
		if a.ReadyBy != "" && !hhmmRe.MatchString(a.ReadyBy) {
			return nil, errors.New("ready_by must be HH:MM on the hour or half-hour, for example 07:30")
		}
		_, device, err := pickDevice(ctx, cl, a.deviceArgs)
		if err != nil {
			return nil, err
		}
		prefs, _ := device["preferences"].(Obj)
		if prefs != nil && octopus.Str(prefs, "unit") != "" && octopus.Str(prefs, "unit") != "PERCENTAGE" {
			return nil, fmt.Errorf("this device's preferences are in %s, not a charge percentage; change them in the Octopus app", octopus.Str(prefs, "unit"))
		}
		current := map[string]Obj{}
		var fallback Obj
		for _, s := range octopus.List(prefs, "schedules") {
			current[octopus.Str(s, "dayOfWeek")] = s
			if fallback == nil {
				fallback = s
			}
		}
		change := map[string]bool{}
		for _, d := range a.Days {
			change[strings.ToUpper(d)] = true
		}
		var schedules []Obj
		for _, day := range weekdays {
			s := current[day]
			if s == nil {
				s = fallback
			}
			target, ready := octopus.Str(s, "max"), shortTime(octopus.Str(s, "time"))
			if len(change) == 0 || change[day] {
				if a.TargetPercent != nil {
					target = fmt.Sprint(*a.TargetPercent)
				}
				if a.ReadyBy != "" {
					ready = a.ReadyBy
				}
			}
			if target == "" || ready == "" {
				return nil, fmt.Errorf("%s has no current schedule to keep; give both target_percent and ready_by", strings.ToLower(day))
			}
			schedules = append(schedules, Obj{"dayOfWeek": day, "time": ready, "max": target})
		}
		var res Obj
		err = cl.GraphQL(ctx, `mutation SetDevicePreferences($input: SmartFlexDevicePreferencesInput!) {
  setDevicePreferences(input: $input) {
    id
    preferences { schedules { dayOfWeek time max } }
  }
}`, Obj{"input": Obj{"deviceId": octopus.Str(device, "id"), "mode": "CHARGE", "unit": "PERCENTAGE", "schedules": schedules}}, &res)
		if err != nil {
			return nil, err
		}
		var after []Obj
		for _, s := range octopus.List(res, "setDevicePreferences", "preferences", "schedules") {
			after = append(after, Obj{"day": octopus.Str(s, "dayOfWeek"), "ready_by": shortTime(octopus.Str(s, "time")), "target": octopus.NumOrNil(s, 0, "max")})
		}
		return Obj{"updated": true, "device_id": octopus.Str(device, "id"), "schedules": after}, nil
	})

	add(c, Def{
		Toolset: "intelligent", Name: "octopus_bump_charge", Title: "Bump charge now",
		Description: "Start (BOOST) or cancel (CANCEL) a bump charge on an Intelligent Octopus car or charger: the car charges straight away, outside the planned smart-charging slots, so the energy is usually charged at your normal (peak) rate. " +
			"Octopus refuses when the car is unplugged, away from home, already full or already boosting. Confirm with the user first.",
		Gate: Write, OpenWorld: true,
		Enums: map[string][]string{"action": {"BOOST", "CANCEL"}},
	}, func(ctx context.Context, a boostArgs) (any, error) {
		action := strings.ToUpper(a.Action)
		if action == "" {
			action = "BOOST"
		}
		_, device, err := pickDevice(ctx, cl, a.deviceArgs)
		if err != nil {
			return nil, err
		}
		var res Obj
		if err := cl.GraphQL(ctx, `mutation UpdateBoostCharge($input: UpdateBoostChargeInput!) {
  updateBoostCharge(input: $input) { id status { currentState } }
}`, Obj{"input": Obj{"deviceId": octopus.Str(device, "id"), "action": action}}, &res); err != nil {
			return nil, err
		}
		return octopus.Compact(Obj{"device_id": octopus.Str(device, "id"), "action": action, "state": octopus.Str(res, "updateBoostCharge", "status", "currentState")}), nil
	})

	add(c, Def{
		Toolset: "intelligent", Name: "octopus_set_smart_charging", Title: "Turn smart charging on or off",
		Description: "Suspend or resume Intelligent Octopus smart control of a car or charger. While suspended (enabled=false), Octopus doesn't schedule charging and the device charges according to its own settings, so home charging may not get the cheap rate. Confirm with the user first.",
		Gate:        Write, Idempotent: true, OpenWorld: true,
	}, func(ctx context.Context, a smartControlArgs) (any, error) {
		_, device, err := pickDevice(ctx, cl, a.deviceArgs)
		if err != nil {
			return nil, err
		}
		action := "SUSPEND"
		if a.Enabled {
			action = "UNSUSPEND"
		}
		var res Obj
		if err := cl.GraphQL(ctx, `mutation UpdateDeviceSmartControl($input: SmartControlInput!) {
  updateDeviceSmartControl(input: $input) { id status { isSuspended currentState } }
}`, Obj{"input": Obj{"deviceId": octopus.Str(device, "id"), "action": action}}, &res); err != nil {
			return nil, err
		}
		return octopus.Compact(Obj{
			"device_id":          octopus.Str(device, "id"),
			"smart_charging":     map[bool]string{true: "on", false: "suspended"}[a.Enabled],
			"state":              octopus.Str(res, "updateDeviceSmartControl", "status", "currentState"),
			"suspended_reported": octopus.Get(res, "updateDeviceSmartControl", "status", "isSuspended"),
		}), nil
	})
}
