package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
)

type savingSessionsArgs struct {
	AccountNumber string `json:"account_number,omitempty" jsonschema:"Account number like A-1234ABCD (default OCTOPUS_ACCOUNT, or the only account the API key can see)"`
	PastLimit     *int   `json:"past_limit,omitempty" jsonschema:"How many past sessions you took part in to include, newest first (default 5)"`
}

type joinSessionArgs struct {
	AccountNumber string `json:"account_number,omitempty" jsonschema:"Account number like A-1234ABCD (default OCTOPUS_ACCOUNT, or the only account the API key can see)"`
	EventCode     string `json:"event_code" jsonschema:"The session's code from octopus_get_saving_sessions"`
}

const savingSessionsQuery = `query SavingSessions($account: String!) {
  savingSessions {
    events(includeDev: false) {
      id
      code
      startAt
      endAt
      rewardPerKwhInOctoPoints
      eventType
      capacityStatus
      targetRegion { regionId }
    }
    account(accountNumber: $account) {
      hasJoinedCampaign
      canJoinCampaign
      tokenBalance
      signedUpMeterPoint { regionId }
      joinedEvents {
        eventId
        startAt
        endAt
        eventType
        eventStatus
        resultsStatus
        rewardGivenInOctoPoints
        energyDeltaKwh
      }
    }
  }
}`

var eventTypeNames = map[string]string{
	"TURN_DOWN":          "Saving Session (use less)",
	"TURN_UP":            "Turn-up session (use more)",
	"WEEKEND_HAPPY_HOUR": "Weekend happy hour",
}

func registerOctoplus(c *Context) {
	cl := c.Client

	loadSessions := func(ctx context.Context, account string) (Obj, error) {
		var res Obj
		if err := cl.Backend(ctx, savingSessionsQuery, Obj{"account": account}, &res); err != nil {
			return nil, err
		}
		ss, _ := res["savingSessions"].(Obj)
		if ss == nil {
			ss = Obj{}
		}
		return ss, nil
	}

	add(c, Def{
		Toolset: "octoplus", Name: "octopus_get_saving_sessions", Title: "Saving Sessions",
		Description: "Get Octoplus Saving Sessions (and turn-up and weekend happy hour sessions): upcoming sessions for your region with their start and end times, the OctoPoints reward per kWh, whether you've joined each one and the code to join it; whether you've signed up to the campaign; your points balance; and results of past sessions you took part in.",
	}, func(ctx context.Context, a savingSessionsArgs) (any, error) {
		number, err := cl.Account(ctx, a.AccountNumber)
		if err != nil {
			return nil, err
		}
		ss, err := loadSessions(ctx, number)
		if err != nil {
			return nil, err
		}
		acct, _ := ss["account"].(Obj)
		region, hasRegion := octopus.Num(acct, "signedUpMeterPoint", "regionId")
		joined := map[string]Obj{}
		for _, j := range octopus.List(acct, "joinedEvents") {
			joined[octopus.Str(j, "eventId")] = j
		}
		now := cl.Now()
		var upcoming []Obj
		for _, e := range octopus.List(ss, "events") {
			end, ok := octopus.ParseTime(octopus.Str(e, "endAt"))
			if ok && end.Before(now) {
				continue
			}
			if hasRegion {
				regions := octopus.List(e, "targetRegion")
				match := len(regions) == 0
				for _, r := range regions {
					if v, ok := octopus.Num(r, "regionId"); ok && v == region {
						match = true
					}
				}
				if !match {
					continue
				}
			}
			start, _ := octopus.ParseTime(octopus.Str(e, "startAt"))
			_, isJoined := joined[octopus.Str(e, "id")]
			upcoming = append(upcoming, octopus.Compact(Obj{
				"code":           octopus.Str(e, "code"),
				"type":           eventTypeNames[octopus.Str(e, "eventType")],
				"start":          octopus.LocalTime(start),
				"end":            octopus.LocalTime(end),
				"points_per_kwh": octopus.Get(e, "rewardPerKwhInOctoPoints"),
				"capacity":       octopus.Str(e, "capacityStatus"),
				"in_progress":    !now.Before(start),
			}))
			upcoming[len(upcoming)-1]["joined"] = isJoined
		}
		sort.SliceStable(upcoming, func(i, j int) bool { return upcoming[i]["start"].(string) < upcoming[j]["start"].(string) })
		var past []Obj
		for _, j := range octopus.List(acct, "joinedEvents") {
			end, ok := octopus.ParseTime(octopus.Str(j, "endAt"))
			if !ok || end.After(now) {
				continue
			}
			start, _ := octopus.ParseTime(octopus.Str(j, "startAt"))
			past = append(past, octopus.Compact(Obj{
				"start":          octopus.LocalTime(start),
				"end":            octopus.LocalTime(end),
				"type":           eventTypeNames[octopus.Str(j, "eventType")],
				"points_earned":  octopus.Get(j, "rewardGivenInOctoPoints"),
				"kwh_difference": octopus.NumOrNil(j, 3, "energyDeltaKwh"),
				"results":        octopus.Str(j, "resultsStatus"),
			}))
		}
		sort.SliceStable(past, func(i, j int) bool { return past[i]["start"].(string) > past[j]["start"].(string) })
		if limit := limitArg(a.PastLimit, 5, 50); len(past) > limit {
			past = past[:limit]
		}
		if upcoming == nil {
			upcoming = []Obj{}
		}
		out := Obj{
			"account_number":    number,
			"signed_up":         octopus.Bool(acct, "hasJoinedCampaign"),
			"points_balance":    octopus.Get(acct, "tokenBalance"),
			"upcoming_sessions": upcoming,
			"past_sessions":     past,
		}
		if !octopus.Bool(acct, "hasJoinedCampaign") {
			out["note"] = "This account hasn't signed up to Saving Sessions. Sign up through Octoplus in the Octopus app before joining sessions."
			if octopus.Bool(acct, "canJoinCampaign") {
				out["can_sign_up"] = true
			}
		}
		if len(upcoming) == 0 {
			out["upcoming_note"] = "No upcoming sessions are announced for your region. Octopus usually announces them a day or so ahead."
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "octoplus", Name: "octopus_join_saving_session", Title: "Join a Saving Session",
		Description: "Join an upcoming Saving Session (or book a weekend happy hour) by its code from octopus_get_saving_sessions. Joining commits to nothing, but the reward depends on using less (or, for turn-up sessions, more) electricity than usual during the session. The account must already be signed up to Saving Sessions in Octoplus.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a joinSessionArgs) (any, error) {
		code := strings.TrimSpace(a.EventCode)
		if code == "" {
			return nil, fmt.Errorf("give event_code from octopus_get_saving_sessions")
		}
		number, err := cl.Account(ctx, a.AccountNumber)
		if err != nil {
			return nil, err
		}
		ss, err := loadSessions(ctx, number)
		if err != nil {
			return nil, err
		}
		var event Obj
		for _, e := range octopus.List(ss, "events") {
			if octopus.Str(e, "code") == code {
				event = e
			}
		}
		if event == nil {
			return nil, fmt.Errorf("no current session has the code %q; check octopus_get_saving_sessions", code)
		}
		vars := Obj{"input": Obj{"accountNumber": number, "eventCode": code}}
		var res Obj
		if octopus.Str(event, "eventType") == "WEEKEND_HAPPY_HOUR" {
			err = cl.Backend(ctx, `mutation BookHappyHour($input: BookSavingSessionsWeekendHappyHourEventInput!) {
  bookSavingSessionsWeekendHappyHourEvent(input: $input) { bookedEvent { eventId startAt endAt } }
}`, vars, &res)
		} else {
			err = cl.Backend(ctx, `mutation JoinSavingSession($input: JoinSavingSessionsEventInput!) {
  joinSavingSessionsEvent(input: $input) { joinedEventCodes }
}`, vars, &res)
		}
		if err != nil {
			return nil, err
		}
		start, _ := octopus.ParseTime(octopus.Str(event, "startAt"))
		end, _ := octopus.ParseTime(octopus.Str(event, "endAt"))
		return Obj{
			"joined": true, "code": code, "type": eventTypeNames[octopus.Str(event, "eventType")],
			"start": octopus.LocalTime(start), "end": octopus.LocalTime(end), "points_per_kwh": octopus.Get(event, "rewardPerKwhInOctoPoints"),
		}, nil
	})

	add(c, Def{
		Toolset: "octoplus", Name: "octopus_get_octoplus", Title: "Octoplus status and points",
		Description: "Get whether the account is enrolled in Octoplus, Octopus's rewards scheme, and its OctoPoints balance with the value Octopus gives it in pounds.",
	}, func(ctx context.Context, a savingSessionsArgs) (any, error) {
		number, err := cl.Account(ctx, a.AccountNumber)
		if err != nil {
			return nil, err
		}
		var res Obj
		err = cl.GraphQL(ctx, `query Octoplus($account: String!) {
  octoplusAccountInfo(accountNumber: $account) { enrollmentStatus }
  loyaltyPointsBalance(input: { accountNumber: $account }) { loyaltyPoints totalMonetaryAmount }
}`, Obj{"account": number}, &res)
		out := Obj{"account_number": number}
		if err != nil {
			if res == nil {
				return nil, err
			}
			out["errors"] = []string{err.Error()}
		}
		status := octopus.Str(res, "octoplusAccountInfo", "enrollmentStatus")
		out["enrollment_status"] = status
		out["enrolled"] = status == "ENROLLED"
		if v := octopus.Get(res, "loyaltyPointsBalance", "loyaltyPoints"); v != nil {
			out["points"] = v
		}
		if v := octopus.Pence(res, "loyaltyPointsBalance", "totalMonetaryAmount"); v != nil {
			out["points_value_gbp"] = v
		}
		return out, nil
	})
}
