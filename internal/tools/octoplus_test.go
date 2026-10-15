package tools

import (
	"strings"
	"testing"

	"github.com/mattoddie/octopus-mcp/internal/octopus/octopustest"
)

func sessionsData() Obj {
	return Obj{"savingSessions": Obj{
		"events": []any{
			Obj{"id": 101, "code": "EVENT_101", "startAt": "2026-10-16T17:00:00+00:00", "endAt": "2026-10-16T18:00:00+00:00", "rewardPerKwhInOctoPoints": 1800, "eventType": "TURN_DOWN", "capacityStatus": "AVAILABLE", "targetRegion": []any{Obj{"regionId": 3}}},
			Obj{"id": 102, "code": "EVENT_102", "startAt": "2026-10-16T17:00:00+00:00", "endAt": "2026-10-16T18:00:00+00:00", "rewardPerKwhInOctoPoints": 1800, "eventType": "TURN_DOWN", "targetRegion": []any{Obj{"regionId": 9}}},
			Obj{"id": 103, "code": "HAPPY_103", "startAt": "2026-10-17T12:00:00+00:00", "endAt": "2026-10-17T13:00:00+00:00", "rewardPerKwhInOctoPoints": 0, "eventType": "WEEKEND_HAPPY_HOUR", "targetRegion": []any{}},
			Obj{"id": 90, "code": "OLD_90", "startAt": "2026-10-01T17:00:00+00:00", "endAt": "2026-10-01T18:00:00+00:00", "eventType": "TURN_DOWN"},
		},
		"account": Obj{
			"hasJoinedCampaign": true, "tokenBalance": 5120, "signedUpMeterPoint": Obj{"regionId": 3},
			"joinedEvents": []any{
				Obj{"eventId": 101, "startAt": "2026-10-16T17:00:00+00:00", "endAt": "2026-10-16T18:00:00+00:00", "eventType": "TURN_DOWN"},
				Obj{"eventId": 90, "startAt": "2026-10-01T17:00:00+00:00", "endAt": "2026-10-01T18:00:00+00:00", "eventType": "TURN_DOWN", "rewardGivenInOctoPoints": 2400, "energyDeltaKwh": -1.333, "resultsStatus": "COMPLETE"},
			},
		},
	}}
}

func TestSavingSessions(t *testing.T) {
	env := newEnv(t)
	env.mock.OnGraphQL(`query SavingSessions`, octopustest.Data(sessionsData()))
	res := env.obj("octopus_get_saving_sessions", nil)
	if r := env.mock.LastGraphQL("SavingSessions"); r.Endpoint != "backend" || r.Variables["account"] != "A-1234ABCD" {
		t.Errorf("saving sessions are on the backend API: %+v", r)
	}
	upcoming := list(res["upcoming_sessions"])
	if len(upcoming) != 2 {
		t.Fatalf("other regions and past events should be filtered: %v", upcoming)
	}
	first := upcoming[0].(Obj)
	if first["code"] != "EVENT_101" || first["joined"] != true || first["start"] != "2026-10-16T18:00:00+01:00" || num(first["points_per_kwh"]) != 1800 {
		t.Errorf("session: %v", first)
	}
	if upcoming[1].(Obj)["joined"] != false || upcoming[1].(Obj)["type"] != "Weekend happy hour" {
		t.Errorf("happy hour: %v", upcoming[1])
	}
	past := list(res["past_sessions"])
	if len(past) != 1 || num(get(past[0], "points_earned")) != 2400 || num(res["points_balance"]) != 5120 {
		t.Errorf("past: %v", res)
	}
}

func TestJoinSavingSession(t *testing.T) {
	env := newEnv(t)
	env.mock.OnGraphQL(`query SavingSessions`, octopustest.Data(sessionsData()))
	env.mock.OnGraphQL(`mutation JoinSavingSession`, octopustest.Data(Obj{"joinSavingSessionsEvent": Obj{"joinedEventCodes": []any{"EVENT_101"}}}))
	env.mock.OnGraphQL(`mutation BookHappyHour`, octopustest.Data(Obj{"bookSavingSessionsWeekendHappyHourEvent": Obj{"bookedEvent": Obj{"eventId": 103}}}))
	res := env.obj("octopus_join_saving_session", map[string]any{"event_code": "EVENT_101"})
	if res["joined"] != true || asJSON(t, env.mock.LastGraphQL("JoinSavingSession").Variables) != `{"input":{"accountNumber":"A-1234ABCD","eventCode":"EVENT_101"}}` {
		t.Errorf("join: %v", res)
	}
	env.obj("octopus_join_saving_session", map[string]any{"event_code": "HAPPY_103"})
	if env.mock.LastGraphQL("BookHappyHour") == nil {
		t.Error("weekend happy hours are booked with their own mutation")
	}
	if msg := env.callErr("octopus_join_saving_session", map[string]any{"event_code": "NOPE"}); !strings.Contains(msg, "no current session") {
		t.Errorf("unknown code: %s", msg)
	}
}

func TestOctoplusPoints(t *testing.T) {
	env := newEnv(t)
	env.mock.OnGraphQL(`query Octoplus`, octopustest.Data(Obj{
		"octoplusAccountInfo":  Obj{"enrollmentStatus": "ENROLLED"},
		"loyaltyPointsBalance": Obj{"loyaltyPoints": 5120, "totalMonetaryAmount": 640},
	}))
	res := env.obj("octopus_get_octoplus", nil)
	if res["enrolled"] != true || num(res["points"]) != 5120 || num(res["points_value_gbp"]) != 6.4 {
		t.Errorf("octoplus: %v", res)
	}
	if r := env.mock.LastGraphQL("Octoplus"); r.Endpoint != "main" {
		t.Errorf("octoplus points are on the Kraken API: %+v", r)
	}
}
