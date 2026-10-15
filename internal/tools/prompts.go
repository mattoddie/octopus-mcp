package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type prompt struct {
	name, title, description string
	args                     []*mcp.PromptArgument
	text                     func(args map[string]string) string
}

func or(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

var prompts = []prompt{
	{
		name: "energy_report", title: "Energy report",
		description: "A plain-English report on your electricity (and gas) use and cost for a period, with the tariff, the expensive times of day and ideas to save.",
		args: []*mcp.PromptArgument{
			{Name: "from", Description: "First day (YYYY-MM-DD; default 30 days ago)"},
			{Name: "to", Description: "Last day (YYYY-MM-DD; default yesterday)"},
		},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`Give me a report on my Octopus Energy use from %s to %s.
1. octopus_get_account: properties, meter points and current tariffs (which tell you whether I'm on a time-of-use tariff like Agile, Go or Intelligent Go).
2. octopus_get_consumption for electricity import, group_by day, for the period; and for gas if I have it, and electricity export if I have solar.
3. octopus_get_consumption again with group_by hour for the last 7 days of the period, to see what time of day I use most.
Report: total kWh and estimated cost (unit costs and standing charges) for each fuel, average pence per kWh, the most expensive days and hours, how much of my electricity fell in peak versus off-peak times if I'm on a time-of-use tariff, and three practical, specific ideas to cut the bill. Mention any gaps in the data and any assumptions the tools flagged (like gas units). Use GBP. Don't change anything.`, or(a["from"], "30 days ago"), or(a["to"], "yesterday"))
		},
	},
	{
		name: "plan_cheap_usage", title: "Plan cheap electricity use",
		description: "Find the cheapest times in the next day or so to run an appliance, charge a car or battery, on Agile or another time-of-use tariff.",
		args: []*mcp.PromptArgument{
			{Name: "job", Description: "What needs running and roughly how long and how much energy (e.g. 'dishwasher, 2 hours, 1.5 kWh')", Required: true},
			{Name: "deadline", Description: "When it must be done by (e.g. 'tomorrow 7am'; default whenever is cheapest in the published prices)"},
		},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`I want to run this as cheaply as possible on my Octopus tariff: %s. Deadline: %s.
1. octopus_get_account to find my electricity import tariff (skip if you already know it).
2. octopus_find_cheapest_slots with duration_hours and kwh from the job (ask me if they aren't clear) and to set to the deadline.
3. If the tariff is flat-rate, say so: timing won't save money (except during Saving Sessions; check octopus_get_saving_sessions).
Tell me the best start time in UK time, the expected cost and how that compares with running it now, plus the runner-up times. Mention any negative-price periods. Don't change anything.`, a["job"], or(a["deadline"], "none"))
		},
	},
	{
		name: "compare_tariffs", title: "Compare tariffs",
		description: "Estimate what your actual recent electricity use would have cost on other Octopus tariffs, such as Agile, Go, Cosy or a fixed deal, against what you're on now.",
		args: []*mcp.PromptArgument{
			{Name: "products", Description: "Product codes or names to compare (default: Agile, Go, Flexible and the cheapest fixed tariff on sale)"},
			{Name: "days", Description: "How many recent days of usage to use (default 30)"},
		},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`Compare my electricity costs across Octopus tariffs using my real usage over the last %s days.
1. octopus_get_account: my region and current electricity import tariff.
2. octopus_get_consumption (group_by half_hour, include_cost) for the period on my current tariff: the baseline. If there are more than 400 half-hours, fetch day by day or use group_by hour with care.
3. octopus_list_products to find the current product codes for: %s.
4. For each product, octopus_get_unit_rates (product_code and my region) over the same period and octopus_get_standing_charges, then price my half-hourly usage against those rates.
Show a table: tariff, unit cost, standing charges, total and difference from my current tariff, and the effective pence per kWh. Note eligibility rules (for example Go and Intelligent Go need an EV or charger, Cosy is for heat pumps) and exit fees on my current tariff (octopus_get_product). Make clear these are estimates from past usage. Don't change anything.`, or(a["days"], "30"), or(a["products"], "Agile, Go, Flexible and the cheapest 12-month fixed"))
		},
	},
	{
		name: "ev_charging_check", title: "EV charging check",
		description: "Check an Intelligent Octopus car or charger: status, schedule, planned and recent charging slots, and anything that needs attention.",
		text: func(map[string]string) string {
			return `Check my Intelligent Octopus EV charging.
1. octopus_list_devices: the car or charger, its state, battery level and schedule (target and ready-by time each day).
2. octopus_get_dispatches: planned slots tonight (times and kWh) and what charged in the last day.
3. octopus_get_unit_rates for my tariff today and tomorrow, to show the off-peak window.
Report: whether everything is working (flag LOST_CONNECTION, SMART_CONTROL_OFF or suspended), when it will charge and roughly how much, and anything I should change. If I ask for a change, use octopus_set_charge_preferences, octopus_bump_charge or octopus_set_smart_charging only after I confirm, and remember a bump charge is usually at the peak rate.`
		},
	},
	{
		name: "saving_session_check", title: "Saving Session check",
		description: "See upcoming Octoplus Saving Sessions, whether you've joined, what you could earn, and how past sessions went.",
		text: func(map[string]string) string {
			return `Check Octoplus Saving Sessions for me.
1. octopus_get_saving_sessions: upcoming sessions in my region, whether I've joined each, the points per kWh, and my past results.
2. octopus_get_octoplus for my points balance and what it's worth.
3. For each upcoming session, octopus_get_consumption (group_by half_hour) for the same times on the previous few weekdays, to estimate my usual use and what I might earn by cutting it.
Report the sessions in UK time with what's at stake, plus practical tips for that time of day. If I haven't joined an upcoming session, offer to join it with octopus_join_saving_session, and only do so after I say yes.`
		},
	},
}

func registerPrompts(s *mcp.Server) {
	for _, p := range prompts {
		p := p
		s.AddPrompt(&mcp.Prompt{Name: p.name, Title: p.title, Description: p.description, Arguments: p.args},
			func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				args := map[string]string{}
				if req.Params != nil && req.Params.Arguments != nil {
					args = req.Params.Arguments
				}
				return &mcp.GetPromptResult{
					Description: p.description,
					Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: p.text(args)}}},
				}, nil
			})
	}
}
