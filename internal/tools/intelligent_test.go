package tools

import (
	"strings"
	"testing"

	"github.com/mattoddie/octopus-mcp/internal/octopus/octopustest"
)

func devicesData() Obj {
	var schedules []any
	for _, d := range weekdays {
		schedules = append(schedules, Obj{"dayOfWeek": d, "time": "07:00:00", "min": nil, "max": 80})
	}
	return Obj{"devices": []any{
		Obj{
			"id": "dev-ev-1", "name": "Family car", "deviceType": "ELECTRIC_VEHICLES", "propertyId": "1001",
			"make": "Example Motors", "model": "Model E", "vehicleBatterySize": "64.0", "chargePointPowerOutput": "7.4",
			"status":      Obj{"current": "LIVE", "currentState": "SMART_CONTROL_CAPABLE", "isSuspended": false, "stateOfCharge": Obj{"value": "54", "timestamp": "2026-10-15T11:30:00+00:00"}},
			"preferences": Obj{"mode": "CHARGE", "unit": "PERCENTAGE", "targetType": "ABSOLUTE_STATE_OF_CHARGE", "schedules": schedules},
		},
		Obj{"id": "dev-hp-1", "deviceType": "HEAT_PUMPS", "status": Obj{"current": "LIVE", "currentState": "SMART_CONTROL_CAPABLE"}},
	}}
}

func TestDevicesAndDispatches(t *testing.T) {
	env := newEnv(t)
	env.mock.OnGraphQL(`query Devices`, octopustest.Data(devicesData()))
	res := env.obj("octopus_list_devices", nil)
	devices := list(res["devices"])
	ev := devices[0].(Obj)
	if len(devices) != 2 || ev["make"] != "Example Motors" || num(ev["state_of_charge"]) != 54 || num(ev["battery_kwh"]) != 64 {
		t.Errorf("devices: %v", devices)
	}
	sched := list(get(ev, "preferences", "schedules"))
	if len(sched) != 7 || get(sched[0], "ready_by") != "07:00" || num(get(sched[0], "target")) != 80 {
		t.Errorf("schedule: %v", sched)
	}

	env.mock.OnGraphQL(`query Dispatches`, octopustest.Data(Obj{
		"flexPlannedDispatches": []any{
			Obj{"start": "2026-10-15T11:30:00+00:00", "end": "2026-10-15T12:30:00+00:00", "type": "SMART", "energyAddedKwh": "3.5"},
			Obj{"start": "2026-10-15T23:30:00+00:00", "end": "2026-10-16T04:30:00+00:00", "type": "SMART", "energyAddedKwh": "20.1"},
		},
		"completedDispatches": []any{
			Obj{"start": "2026-10-14T23:30:00+00:00", "end": "2026-10-15T01:00:00+00:00", "delta": "-9.5", "meta": Obj{"source": "smart-charge", "location": "AT_HOME"}},
			Obj{"start": "2026-10-01T23:30:00+00:00", "end": "2026-10-02T01:00:00+00:00", "delta": "-4"},
		},
	}))
	d := env.obj("octopus_get_dispatches", nil)
	if d["in_dispatch_now"] != true || num(d["planned_total_kwh"]) != 23.6 || len(list(d["planned"])) != 2 || len(list(d["completed"])) != 1 {
		t.Errorf("dispatches: %v", d)
	}
	vars := env.mock.LastGraphQL("query Dispatches").Variables
	if vars["device"] != "dev-ev-1" || vars["account"] != "A-1234ABCD" {
		t.Errorf("dispatch variables: %v", vars)
	}
	// Planned dispatches can fail (KT-CT-4340) while completed ones work: keep what came back.
	env.mock.OnGraphQL(`query Dispatches`, func(octopustest.GraphQLRequest) (any, []any) {
		return Obj{"flexPlannedDispatches": nil, "completedDispatches": []any{}}, []any{Obj{"message": "Unable to fetch planned dispatches.", "extensions": Obj{"errorCode": "KT-CT-4340"}}}
	})
	if d := env.obj("octopus_get_dispatches", nil); !strings.Contains(asJSON(t, d["errors"]), "KT-CT-4340") {
		t.Errorf("partial dispatches: %v", d)
	}
}

func TestSetChargePreferences(t *testing.T) {
	env := newEnv(t)
	env.mock.OnGraphQL(`query Devices`, octopustest.Data(devicesData()))
	env.mock.OnGraphQL(`mutation SetDevicePreferences`, func(r octopustest.GraphQLRequest) (any, []any) {
		return Obj{"setDevicePreferences": Obj{"id": "dev-ev-1", "preferences": Obj{"schedules": get(r.Variables, "input", "schedules")}}}, nil
	})
	res := env.obj("octopus_set_charge_preferences", map[string]any{"target_percent": 90, "ready_by": "06:30", "days": []string{"SATURDAY", "SUNDAY"}})
	input := env.mock.LastGraphQL("mutation SetDevicePreferences").Variables["input"].(map[string]any)
	if input["deviceId"] != "dev-ev-1" || input["mode"] != "CHARGE" || input["unit"] != "PERCENTAGE" {
		t.Errorf("input: %v", input)
	}
	scheds := list(input["schedules"])
	if len(scheds) != 7 || asJSON(t, scheds[0]) != `{"dayOfWeek":"MONDAY","max":"80","time":"07:00"}` || asJSON(t, scheds[6]) != `{"dayOfWeek":"SUNDAY","max":"90","time":"06:30"}` {
		t.Errorf("schedules: %v", scheds)
	}
	if res["updated"] != true || len(list(res["schedules"])) != 7 {
		t.Errorf("result: %v", res)
	}
	for args, want := range map[string]map[string]any{
		"give target_percent":      {},
		"between 10 and 100":       {"target_percent": 5},
		"on the hour or half-hour": {"ready_by": "7:15"},
	} {
		if msg := env.callErr("octopus_set_charge_preferences", want); !strings.Contains(msg, args) {
			t.Errorf("%v: %s", want, msg)
		}
	}
}

func TestBumpChargeAndSmartControl(t *testing.T) {
	env := newEnv(t)
	env.mock.OnGraphQL(`query Devices`, octopustest.Data(devicesData()))
	env.mock.OnGraphQL(`mutation UpdateBoostCharge`, octopustest.Data(Obj{"updateBoostCharge": Obj{"id": "dev-ev-1", "status": Obj{"currentState": "BOOSTING"}}}))
	res := env.obj("octopus_bump_charge", nil)
	if res["state"] != "BOOSTING" || asJSON(t, env.mock.LastGraphQL("UpdateBoostCharge").Variables) != `{"input":{"action":"BOOST","deviceId":"dev-ev-1"}}` {
		t.Errorf("boost: %v", res)
	}
	env.mock.OnGraphQL(`mutation UpdateBoostCharge`, func(octopustest.GraphQLRequest) (any, []any) {
		return Obj{"updateBoostCharge": nil}, []any{Obj{"message": "Unable to trigger boost charge.", "extensions": Obj{"errorCode": "KT-CT-4357", "boostChargeRefusalReasons": []any{"BC_DEVICE_DISCONNECTED"}}}}
	})
	if msg := env.callErr("octopus_bump_charge", nil); !strings.Contains(msg, "BC_DEVICE_DISCONNECTED") {
		t.Errorf("refusal: %s", msg)
	}

	env.mock.OnGraphQL(`mutation UpdateDeviceSmartControl`, octopustest.Data(Obj{"updateDeviceSmartControl": Obj{"id": "dev-ev-1", "status": Obj{"isSuspended": true, "currentState": "SMART_CONTROL_OFF"}}}))
	res = env.obj("octopus_set_smart_charging", map[string]any{"enabled": false})
	if res["smart_charging"] != "suspended" || get(env.mock.LastGraphQL("UpdateDeviceSmartControl").Variables, "input", "action") != "SUSPEND" {
		t.Errorf("smart control: %v", res)
	}
	if msg := env.callErr("octopus_bump_charge", map[string]any{"device_id": "nope"}); !strings.Contains(msg, "isn't on account") {
		t.Errorf("unknown device: %s", msg)
	}
}
