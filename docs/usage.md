# Using octopus-mcp

Once the server is connected, just talk to your assistant about your energy. This page shows what works well and explains how the tools behave, so you know what to expect.

- [Example questions](#example-questions)
- [Example conversations](#example-conversations)
- [Built-in prompts](#built-in-prompts)
- [Units, prices and times](#units-prices-and-times)
- [How cost estimates work](#how-cost-estimates-work)
- [Gas readings in cubic metres](#gas-readings-in-cubic-metres)
- [Intelligent Octopus](#intelligent-octopus)
- [Saving Sessions and Octoplus](#saving-sessions-and-octoplus)
- [The raw API tools](#the-raw-api-tools)
- [Tips for good results](#tips-for-good-results)

## Example questions

**Your account**

- *"What tariff am I on, and when did it start?"* (`octopus_get_account`)
- *"What's my balance? Am I in credit?"* (`octopus_get_balance`)
- *"Show me my last three bills."* (`octopus_list_bills`)

**Usage and cost**

- *"How much electricity did I use yesterday, hour by hour?"* (`octopus_get_consumption`)
- *"What did my gas cost last month?"*
- *"How much did my solar panels export this week, and what did Octopus pay me?"*
- *"What's the house drawing right now?"* (`octopus_get_live_usage`, needs a Home Mini)

**Prices**

- *"What are the Agile prices for tomorrow evening?"* (`octopus_get_unit_rates`)
- *"When's the cheapest 3-hour window overnight to charge the battery?"* (`octopus_find_cheapest_slots`)
- *"How much is the standing charge on the Octopus 12M Fixed in London?"* (`octopus_get_product`)
- *"Would Agile have been cheaper than Flexible for me last month?"*

**EV charging and rewards**

- *"When will the car charge tonight?"* (`octopus_get_dispatches`)
- *"Set the car to 90% by 7am on weekdays."* (writes)
- *"Is there a Saving Session coming up? Join it."* (writes)

## Example conversations

These show the kind of back-and-forth to expect. Your assistant's wording will differ.

### Planning around Agile prices

> **You:** *I need to run the washing machine and the dishwasher, about 2 hours and 2.5 kWh in total. When's cheapest before 7am?*
>
> **Assistant:** *The cheapest two hours are 02:00 to 04:00, averaging 8.4p/kWh, so about 21p for both loads. Running them now (19:30, 31.2p/kWh) would cost about 78p. The next best windows are 00:30 to 02:30 (9.1p) and 04:00 to 06:00 (10.3p). Prices from 05:30 are rising as the morning peak starts.*

Behind the scenes the assistant used `octopus_get_account` to find your Agile tariff and region, then `octopus_find_cheapest_slots` with `duration_hours: 2`, `kwh: 2.5` and `to: "tomorrow 07:00"`. The `plan_cheap_usage` prompt asks for exactly this flow.

### A monthly report

> **You:** */mcp__octopus__energy_report 2026-09-01 2026-09-30*
>
> **Assistant:** *In September you used 241 kWh of electricity for £52.80 including £12.36 of standing charges, an average of 16.8p/kWh on Agile. Your most expensive days were the 12th and 19th, when you ran the tumble dryer between 4pm and 7pm. Moving that to after 9pm would have saved about £4. Gas: 412 kWh (37.1 m³ from your SMETS2 meter), £30.50…*

### EV charging

> **You:** *Is the car going to be ready for the school run?*
>
> **Assistant:** *Yes. The car is plugged in at 54% and set to reach 80% by 07:30. Octopus has planned two charging slots: 23:30 to 01:30 (12.1 kWh) and 03:00 to 05:00 (9.8 kWh), both at your off-peak rate.*
>
> **You:** *Make it 90% just for tomorrow, it's a long drive.*
>
> **Assistant:** *I'll change Friday's target from 80% to 90%, keeping the 07:30 ready-by time. The other days stay as they are. Go ahead?*

With `OCTOPUS_ALLOW_WRITES=true`, the assistant then calls `octopus_set_charge_preferences`, and Octopus replans the charge.

## Built-in prompts

The server includes five prompts. Each one is a ready-made, multi-step request that tells the model which tools to use and how to report. Most clients show them as slash commands or in a prompt picker. In Claude Code they're `/mcp__octopus__<name>`.

| Prompt | Arguments | What it does |
|---|---|---|
| `energy_report` | `from`, `to` (optional; default the last 30 days) | Usage and cost for each fuel, the expensive days and hours, and ideas to save. |
| `plan_cheap_usage` | `job`, `deadline` (optional) | Finds the cheapest time to run an appliance or charge a car or battery, and compares it with running now. |
| `compare_tariffs` | `products`, `days` (both optional) | Prices your real recent usage on other Octopus tariffs and compares them with your current one. |
| `ev_charging_check` | | Checks an Intelligent Octopus car or charger: status, schedule, planned and recent charging. |
| `saving_session_check` | | Upcoming Saving Sessions, what you could earn and how past sessions went. Offers to join, and only does so when you say yes. |

## Units, prices and times

- **Unit rates** are in **pence per kWh** and **standing charges** in **pence per day**, both **including VAT** (5% on domestic energy). Fields called `exc_vat` give the price without VAT.
- **Money totals**, such as cost estimates, balances and bills, are in **pounds** (`*_gbp`).
- **Electricity** is in **kWh**. **Gas** depends on the meter. See [Gas readings in cubic metres](#gas-readings-in-cubic-metres).
- **Times** are shown in **UK time** (GMT or BST) with the offset, for example `2026-10-15T17:30:00+01:00`.
- **Time arguments** take a date (`2026-10-15`, a whole UK day), a date and time (`2026-10-15T17:30`, UK time), a full timestamp with an offset, or `now`, `today`, `yesterday` and `tomorrow`. An end date is inclusive: `to: "2026-10-15"` includes all of the 15th.
- **Balances** are as Octopus shows them: positive means you're **in credit**, negative means you owe Octopus.
- **Regions.** Prices vary across the 14 electricity regions, lettered A to P. Your tariff code ends with your region, and `octopus_find_region` looks one up by postcode.

## How cost estimates work

`octopus_get_consumption` estimates the cost of what you used (or, for export, what Octopus paid you):

1. It fetches your half-hourly meter readings for the period.
2. For each half-hour, it finds the tariff you were on at the time (from your account's agreements) and that tariff's unit rate for that half-hour. On Agile, Go, Intelligent Go, Cosy and Flux that's the actual time-of-use price, and on Tracker the day's price.
3. It adds the daily standing charge for each day in the period.

That's close to how Octopus bills, but it's still an estimate:

- It doesn't include discounts, credits, Octoplus rewards or the cheaper rate Intelligent Octopus Go gives during extra smart-charging slots outside the normal off-peak hours.
- For **day/night (Economy 7)** tariffs, Octopus's API doesn't say which hours your meter treats as night. octopus-mcp assumes 00:30 to 07:30 GMT, a common pattern, and says so.
- Missing readings aren't estimated. `half_hours_with_data` and `half_hours_in_period` show how complete the data is.
- Meter readings usually arrive the next day, so "today" is often empty.

Your bill is always the source of truth.

## Gas readings in cubic metres

Smart gas meters report in different units:

- **SMETS2** meters report the **volume** of gas, in **m³**.
- **SMETS1** meters report **energy**, in **kWh**, already converted.

Octopus's REST API returns whatever the meter reports, without saying which. octopus-mcp asks the GraphQL API for the meter's units, which `octopus_get_account` shows as `consumption_units`. If Octopus doesn't say, it assumes m³ and says so in `assumptions`.

To cost gas in m³, octopus-mcp converts it to kWh with the standard formula Octopus and other suppliers use:

> kWh = m³ × 1.02264 (volume correction) × calorific value (MJ/m³) ÷ 3.6

The calorific value varies a little by region and month. octopus-mcp uses **39.5 MJ/m³** unless you pass `calorific_value`. Your bill shows the actual figure. If your meter actually reports kWh, pass `gas_units: "kwh"`.

## Intelligent Octopus

The `intelligent` toolset works with the cars, chargers and other devices Octopus controls on smart tariffs like **Intelligent Octopus Go**:

- `octopus_list_devices` shows each device's status, battery level (where the car reports it) and charging schedule.
- `octopus_get_dispatches` shows the planned charging slots and the energy each should add, and recent completed slots (Octopus reports these about 12 hours late). During planned smart-charging slots, Intelligent Octopus Go usually charges the whole home at the off-peak rate, even outside the normal off-peak hours. Check your tariff's terms for the limits.

With `OCTOPUS_ALLOW_WRITES=true`:

- `octopus_set_charge_preferences` changes the target charge and ready-by time for every day or chosen days. The Octopus app offers ready-by times from 04:00 to 11:00, and Octopus may reject values outside what the app allows.
- `octopus_bump_charge` starts charging straight away (or cancels a bump). **A bump charge is usually charged at your normal, peak rate.** Octopus refuses if the car is unplugged, away, full or already boosting, and octopus-mcp passes on its reasons.
- `octopus_set_smart_charging` suspends or resumes smart control. While it's suspended, the device charges on its own settings, so it may not get the cheap rate.

The server tells the model to confirm these with you first, and `octopus_bump_charge` and `octopus_set_smart_charging` are marked *open-world* because they affect a real car.

## Saving Sessions and Octoplus

`octopus_get_saving_sessions` lists upcoming Saving Sessions (and turn-up and weekend happy hour sessions) for your region: the times, the OctoPoints per kWh, whether you've joined, and the code to join. It also shows your points balance and past results.

With writes on, `octopus_join_saving_session` joins one by its code (or books a weekend happy hour). Your account must already be signed up to Saving Sessions through Octoplus in the Octopus app.

`octopus_get_octoplus` shows whether you're enrolled in Octoplus and your points balance with the value Octopus gives it.

Saving Sessions come from a separate Octopus Energy API (`api.backend.octopus.energy`) that the server reaches with the same Kraken token.

## The raw API tools

The `raw` toolset is a fallback for things the other tools don't cover:

- `octopus_graphql` runs any query against the Kraken GraphQL API, or the backend API with `endpoint: "backend"`. Mutations need `OCTOPUS_ALLOW_WRITES=true`. Operations that issue or change tokens, API keys or passwords are always refused. The schema can be browsed at [api.octopus.energy/v1/graphql/](https://api.octopus.energy/v1/graphql/).
- `octopus_rest_get` fetches any REST path, such as `/electricity-meter-points/<MPAN>/`. The REST API is read-only.

Leave `raw` out of `OCTOPUS_TOOLSETS` if you want the assistant limited to the curated tools.

## Tips for good results

- **Be specific about periods.** *"Last week"*, *"September"* or *"since 1 October"* lead to tighter tool calls than *"recently"*.
- **Ask for evidence.** *"…and show me the days you based that on"* keeps answers grounded.
- **Use coarse groupings for long periods.** A year of half-hours is 17,520 rows. Ask for days, weeks or months, and drill in where it matters.
- **Plan, then apply.** For EV and Saving Session changes, ask what would change, check it, then say *"go ahead"*.
- **It's not financial advice.** Tariff comparisons are estimates from past usage, and they don't know about eligibility rules or exit fees unless you ask. Check before you switch.
