package tools

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
)

type accountArgs struct {
	AccountNumber  string `json:"account_number,omitempty" jsonschema:"Account number like A-1234ABCD (default OCTOPUS_ACCOUNT, or the only account the API key can see)"`
	IncludeHistory bool   `json:"include_history,omitempty" jsonschema:"Also list previous tariffs and properties you have moved out of"`
	IncludeBalance *bool  `json:"include_balance,omitempty" jsonschema:"Include the account balance (default true)"`
	Raw            bool   `json:"raw,omitempty" jsonschema:"Return the REST account object unchanged instead of the summary"`
}

type accountNumberArgs struct {
	AccountNumber string `json:"account_number,omitempty" jsonschema:"Account number like A-1234ABCD (default OCTOPUS_ACCOUNT, or the only account the API key can see)"`
}

type transactionsArgs struct {
	AccountNumber string `json:"account_number,omitempty" jsonschema:"Account number like A-1234ABCD (default OCTOPUS_ACCOUNT, or the only account the API key can see)"`
	Limit         *int   `json:"limit,omitempty" jsonschema:"How many to return, newest first (default 20, at most 99)"`
	From          string `json:"from,omitempty" jsonschema:"Only transactions posted on or after this date (YYYY-MM-DD)"`
	To            string `json:"to,omitempty" jsonschema:"Only transactions posted on or before this date (YYYY-MM-DD)"`
}

type billsArgs struct {
	AccountNumber string `json:"account_number,omitempty" jsonschema:"Account number like A-1234ABCD (default OCTOPUS_ACCOUNT, or the only account the API key can see)"`
	Limit         *int   `json:"limit,omitempty" jsonschema:"How many to return, newest first (default 6, at most 50)"`
	From          string `json:"from,omitempty" jsonschema:"Only bills issued on or after this date (YYYY-MM-DD)"`
	To            string `json:"to,omitempty" jsonschema:"Only bills issued on or before this date (YYYY-MM-DD)"`
	IncludeLinks  bool   `json:"include_pdf_links,omitempty" jsonschema:"Include a temporary download link for each bill's PDF (the link expires after a short time)"`
}

const balanceQuery = `query Balance($account: String!) {
  account(accountNumber: $account) {
    number
    status
    balance
    overdueBalance
  }
}`

const billsQuery = `query Bills($account: String!, $first: Int!, $from: Date, $to: Date, $links: Boolean!) {
  account(accountNumber: $account) {
    bills(first: $first, orderBy: ISSUED_DATE_DESC, issuedFromDate: $from, issuedToDate: $to) {
      totalCount
      edges {
        node {
          id
          billType
          fromDate
          toDate
          issuedDate
          ... on StatementType {
            openingBalance
            closingBalance
            totalCharges { netTotal taxTotal grossTotal }
            totalCredits { grossTotal }
          }
          ... on InvoiceType { grossAmount }
          attachments(first: 1) @include(if: $links) {
            edges { node { filename temporaryUrl temporaryUrlExpiresAt } }
          }
        }
      }
    }
  }
}`

const transactionsQuery = `query Transactions($account: String!, $first: Int!, $from: Date, $to: Date) {
  account(accountNumber: $account) {
    transactions(first: $first, fromDate: $from, toDate: $to, orderBy: POSTED_DATE_DESC) {
      totalCount
      edges {
        node {
          __typename
          id
          postedDate
          title
          amounts { net tax gross }
          balanceCarriedForward
          isHeld
          isReversed
          note
        }
      }
    }
  }
}`

func summarizeBalance(a Obj) Obj {
	out := octopus.Compact(Obj{
		"account_number":      octopus.Str(a, "number"),
		"status":              octopus.Str(a, "status"),
		"balance_gbp":         octopus.Pence(a, "balance"),
		"overdue_balance_gbp": octopus.Pence(a, "overdueBalance"),
	})
	if b, ok := octopus.Num(a, "balance"); ok {
		out["in_credit"] = b >= 0
		out["note"] = "A positive balance means you are in credit; negative means you owe Octopus."
	}
	return out
}

func registerAccount(c *Context) {
	cl := c.Client

	add(c, Def{
		Toolset: "account", Name: "octopus_get_account", Title: "Account overview",
		Description: "A one-call overview of the Octopus Energy account and a good first call: each property's address, its electricity meter points (MPAN, import or export, meter serial numbers, smart meter device IDs) and gas meter points (MPRN, serials, whether the meter reports in kWh or m³), the current tariff on each (tariff and product codes, region), the estimated annual consumption Octopus holds, and the account balance. " +
			"Use the tariff codes with the tariffs tools, and the MPAN/MPRN and serials with octopus_get_consumption.",
	}, func(ctx context.Context, a accountArgs) (any, error) {
		number, acct, mps, err := loadAccount(ctx, cl, a.AccountNumber)
		if err != nil {
			return nil, err
		}
		if a.Raw {
			return acct, nil
		}
		var (
			wg                 sync.WaitGroup
			devices            map[string]smartDevice
			balance            Obj
			errDevices, errBal error
			wantBalance        = a.IncludeBalance == nil || *a.IncludeBalance
		)
		wg.Add(1)
		go func() { defer wg.Done(); devices, errDevices = loadSmartDevices(ctx, cl, number) }()
		if wantBalance {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var res Obj
				errBal = cl.GraphQL(ctx, balanceQuery, Obj{"account": number}, &res)
				balance, _ = res["account"].(Obj)
			}()
		}
		wg.Wait()

		now := cl.Now()
		out := Obj{"account_number": number}
		var errs []string
		if errDevices != nil {
			errs = append(errs, "smart meter details: "+errDevices.Error())
		}
		if wantBalance {
			if errBal != nil {
				errs = append(errs, "balance: "+errBal.Error())
			} else if balance != nil {
				b := summarizeBalance(balance)
				delete(b, "account_number")
				out["balance"] = b
			}
		}
		var properties []Obj
		for _, p := range octopus.List(acct, "properties") {
			if octopus.Str(p, "moved_out_at") != "" && !a.IncludeHistory {
				continue
			}
			prop := octopus.Compact(Obj{
				"id":        octopus.Get(p, "id"),
				"address":   propertyAddress(p),
				"moved_in":  octopus.Str(p, "moved_in_at"),
				"moved_out": octopus.Str(p, "moved_out_at"),
			})
			var elec, gas []Obj
			for _, mp := range mps {
				if mp.PropertyID != octopus.Str(p, "id") {
					continue
				}
				s := summarizeMeterPoint(mp, devices, now, a.IncludeHistory)
				if mp.Fuel == "gas" {
					gas = append(gas, s)
				} else {
					elec = append(elec, s)
				}
			}
			if len(elec) > 0 {
				prop["electricity_meter_points"] = elec
			}
			if len(gas) > 0 {
				prop["gas_meter_points"] = gas
			}
			properties = append(properties, prop)
		}
		out["properties"] = properties
		if len(errs) > 0 {
			out["errors"] = errs
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "account", Name: "octopus_get_balance", Title: "Account balance",
		Description: "Get the account balance in pounds (positive means in credit, negative means you owe Octopus), any overdue balance and the account status.",
	}, func(ctx context.Context, a accountNumberArgs) (any, error) {
		number, err := cl.Account(ctx, a.AccountNumber)
		if err != nil {
			return nil, err
		}
		var res Obj
		if err := cl.GraphQL(ctx, balanceQuery, Obj{"account": number}, &res); err != nil {
			return nil, err
		}
		acct, _ := res["account"].(Obj)
		return summarizeBalance(acct), nil
	})

	add(c, Def{
		Toolset: "account", Name: "octopus_list_bills", Title: "List bills",
		Description: "List the account's bills (statements), newest first: the period covered, issue date, charges and credits, and the opening and closing balances, all in pounds. Set include_pdf_links for a temporary link to each bill's PDF.",
	}, func(ctx context.Context, a billsArgs) (any, error) {
		number, err := cl.Account(ctx, a.AccountNumber)
		if err != nil {
			return nil, err
		}
		vars := Obj{"account": number, "first": limitArg(a.Limit, 6, 50), "links": a.IncludeLinks}
		if err := dateVars(vars, a.From, a.To, cl.Now()); err != nil {
			return nil, err
		}
		var res Obj
		if err := cl.GraphQL(ctx, billsQuery, vars, &res); err != nil {
			return nil, err
		}
		var bills []Obj
		for _, b := range octopus.Edges(res, "account", "bills") {
			bill := octopus.Compact(Obj{
				"id":                  octopus.Str(b, "id"),
				"type":                octopus.Str(b, "billType"),
				"from":                octopus.Str(b, "fromDate"),
				"to":                  octopus.Str(b, "toDate"),
				"issued":              octopus.Str(b, "issuedDate"),
				"charges_gbp":         octopus.Pence(b, "totalCharges", "grossTotal"),
				"charges_exc_vat_gbp": octopus.Pence(b, "totalCharges", "netTotal"),
				"vat_gbp":             octopus.Pence(b, "totalCharges", "taxTotal"),
				"credits_gbp":         octopus.Pence(b, "totalCredits", "grossTotal"),
				"amount_gbp":          octopus.Pence(b, "grossAmount"),
				"opening_balance_gbp": octopus.Pence(b, "openingBalance"),
				"closing_balance_gbp": octopus.Pence(b, "closingBalance"),
			})
			for _, att := range octopus.Edges(b, "attachments") {
				bill["pdf"] = octopus.Compact(Obj{"file_name": octopus.Str(att, "filename"), "url": octopus.Str(att, "temporaryUrl"), "expires": octopus.Str(att, "temporaryUrlExpiresAt")})
			}
			bills = append(bills, bill)
		}
		if bills == nil {
			bills = []Obj{}
		}
		return Obj{"account_number": number, "total": octopus.Get(res, "account", "bills", "totalCount"), "returned": len(bills), "bills": bills,
			"note": "Balances are as Octopus shows them: positive means in credit."}, nil
	})

	add(c, Def{
		Toolset: "account", Name: "octopus_list_transactions", Title: "List transactions",
		Description: "List the account's transactions, newest first: charges for energy, payments (such as direct debits), credits (such as Octoplus rewards or Saving Session payouts) and refunds, in pounds, with the balance after each.",
	}, func(ctx context.Context, a transactionsArgs) (any, error) {
		number, err := cl.Account(ctx, a.AccountNumber)
		if err != nil {
			return nil, err
		}
		vars := Obj{"account": number, "first": limitArg(a.Limit, 20, 99)}
		if err := dateVars(vars, a.From, a.To, cl.Now()); err != nil {
			return nil, err
		}
		var res Obj
		if err := cl.GraphQL(ctx, transactionsQuery, vars, &res); err != nil {
			return nil, err
		}
		var items []Obj
		for _, t := range octopus.Edges(res, "account", "transactions") {
			items = append(items, octopus.Compact(Obj{
				"id":                octopus.Str(t, "id"),
				"date":              octopus.Str(t, "postedDate"),
				"type":              strings.ToLower(octopus.Str(t, "__typename")),
				"title":             octopus.Str(t, "title"),
				"amount_gbp":        firstNonNil(octopus.Pence(t, "amounts", "gross"), octopus.Pence(t, "amounts", "net")),
				"vat_gbp":           octopus.Pence(t, "amounts", "tax"),
				"balance_after_gbp": octopus.Pence(t, "balanceCarriedForward"),
				"held":              octopus.Bool(t, "isHeld"),
				"reversed":          octopus.Bool(t, "isReversed"),
				"note":              octopus.Str(t, "note"),
			}))
		}
		if items == nil {
			items = []Obj{}
		}
		return Obj{"account_number": number, "total": octopus.Get(res, "account", "transactions", "totalCount"), "returned": len(items), "transactions": items}, nil
	})
}

func firstNonNil(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func limitArg(limit *int, def, maxN int) int {
	if limit == nil || *limit <= 0 {
		return def
	}
	return min(*limit, maxN)
}

// dateVars adds GraphQL Date variables from date arguments.
func dateVars(vars Obj, from, to string, now time.Time) error {
	if from != "" {
		t, err := parseWhen(from, false, now)
		if err != nil {
			return err
		}
		vars["from"] = t.In(octopus.London).Format("2006-01-02")
	}
	if to != "" {
		t, err := parseWhen(to, false, now)
		if err != nil {
			return err
		}
		vars["to"] = t.In(octopus.London).Format("2006-01-02")
	}
	return nil
}

// summarizeMeterPoint is the overview of a meter point with its current tariff.
func summarizeMeterPoint(mp *meterPoint, devices map[string]smartDevice, now time.Time, history bool) Obj {
	var meters []Obj
	units := ""
	for _, s := range mp.Serials {
		m := Obj{"serial": s}
		if d, ok := devices[s]; ok {
			m = octopus.Compact(Obj{"serial": s, "smart_device_id": d.DeviceID, "export_device_id": d.ExportDeviceID, "meter_type": d.MeterType, "reports_in": d.Units})
			if d.Units != "" {
				units = d.Units
			}
		}
		meters = append(meters, m)
	}
	out := Obj{"meters": meters}
	if mp.Fuel == "gas" {
		out["mprn"] = mp.ID
		if units == "" {
			units = "unknown (SMETS2 gas meters usually report m³; SMETS1 meters kWh)"
		}
		out["consumption_units"] = units
	} else {
		out["mpan"] = mp.ID
		out["direction"] = mp.direction()
		if v := octopus.Get(mp.Raw, "profile_class"); v != nil {
			out["profile_class"] = v
		}
	}
	if v, ok := octopus.Num(mp.Raw, "consumption_standard"); ok && v > 0 {
		out["estimated_annual_kwh"] = v
	}
	if cur := mp.agreementAt(now); cur != nil {
		t := cur.summary()
		for _, s := range mp.Serials {
			if d, ok := devices[s]; ok && d.TariffName != "" {
				t["name"] = d.TariffName
				break
			}
		}
		out["tariff"] = t
	} else {
		out["tariff"] = nil
		out["note"] = "No tariff is active today on this meter point (it may have just switched, or be a past supply)."
	}
	if history && len(mp.Agreements) > 1 {
		var past []Obj
		for i := len(mp.Agreements) - 1; i >= 0; i-- {
			if !mp.Agreements[i].activeAt(now) {
				past = append(past, mp.Agreements[i].summary())
			}
		}
		out["previous_tariffs"] = past
	}
	return out
}
