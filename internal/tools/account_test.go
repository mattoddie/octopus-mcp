package tools

import (
	"strings"
	"testing"

	"github.com/mattoddie/octopus-mcp/internal/octopus/octopustest"
)

func TestGetAccountOverview(t *testing.T) {
	env := newEnv(t)
	env.withAccount()
	env.mock.OnGraphQL(`query Balance`, octopustest.Data(Obj{"account": Obj{"number": "A-1234ABCD", "status": "ACTIVE", "balance": -4250, "overdueBalance": 0}}))

	res := env.obj("octopus_get_account", nil)
	if res["account_number"] != "A-1234ABCD" {
		t.Errorf("account: %v", res["account_number"])
	}
	props := list(res["properties"])
	if len(props) != 1 {
		t.Fatalf("moved-out properties should be hidden: %v", props)
	}
	p := props[0].(Obj)
	if p["address"] != "1 Example Street, Exampleton, EX1 1AA" {
		t.Errorf("address: %v", p["address"])
	}
	elec := list(p["electricity_meter_points"])
	imp := elec[0].(Obj)
	tariff := imp["tariff"].(Obj)
	if imp["mpan"] != "1000000000001" || imp["direction"] != "import" || tariff["tariff_code"] != "E-1R-AGILE-24-10-01-C" ||
		tariff["product_code"] != "AGILE-24-10-01" || tariff["region_name"] != "London" || tariff["name"] != "Agile Octopus" {
		t.Errorf("import meter point: %v", imp)
	}
	meter := list(imp["meters"])[0].(Obj)
	if meter["serial"] != "21E0000001" || meter["smart_device_id"] != "00-11-22-33-44-55-66-77" {
		t.Errorf("meter: %v", meter)
	}
	if elec[1].(Obj)["direction"] != "export" {
		t.Errorf("export meter point: %v", elec[1])
	}
	gas := list(p["gas_meter_points"])[0].(Obj)
	if gas["mprn"] != "3000000001" || gas["consumption_units"] != "m³" || num(gas["estimated_annual_kwh"]) != 11500 {
		t.Errorf("gas: %v", gas)
	}
	bal := res["balance"].(Obj)
	if num(bal["balance_gbp"]) != -42.5 || bal["in_credit"] != false {
		t.Errorf("balance: %v", bal)
	}
	// REST requests use Basic auth with the API key.
	if r := env.mock.Last("/accounts/"); r == nil || r.BasicUser != octopustest.APIKey {
		t.Errorf("account request auth: %+v", r)
	}

	hist := env.obj("octopus_get_account", map[string]any{"include_history": true, "include_balance": false})
	if len(list(hist["properties"])) != 2 || hist["balance"] != nil {
		t.Errorf("history: %v", hist)
	}
	imp = list(list(hist["properties"])[0].(Obj)["electricity_meter_points"])[0].(Obj)
	if prev := list(imp["previous_tariffs"]); len(prev) != 1 || prev[0].(Obj)["tariff_code"] != "E-1R-VAR-22-11-01-C" {
		t.Errorf("previous tariffs: %v", imp["previous_tariffs"])
	}
}

func TestGetAccountKeepsGoingWhenGraphQLFails(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/accounts/A-1234ABCD/", octopustest.JSON(jsonObj(t, accountFixture)))
	env.mock.OnGraphQL(`.`, octopustest.Errors("Unable to fetch", "KT-CT-9999"))
	res := env.obj("octopus_get_account", nil)
	if len(list(res["errors"])) != 2 || len(list(res["properties"])) != 1 {
		t.Errorf("partial result: %v", res)
	}
}

func TestBalanceBillsAndTransactions(t *testing.T) {
	env := newEnv(t)
	env.mock.OnGraphQL(`query Balance`, octopustest.Data(Obj{"account": Obj{"number": "A-1234ABCD", "status": "ACTIVE", "balance": 12345, "overdueBalance": 0}}))
	bal := env.obj("octopus_get_balance", nil)
	if num(bal["balance_gbp"]) != 123.45 || bal["in_credit"] != true {
		t.Errorf("balance: %v", bal)
	}

	env.mock.OnGraphQL(`query Bills`, octopustest.Data(Obj{"account": Obj{"bills": Obj{"totalCount": 14, "edges": []any{Obj{"node": Obj{
		"id": "123", "billType": "STATEMENT", "fromDate": "2026-09-01", "toDate": "2026-09-30", "issuedDate": "2026-10-02",
		"openingBalance": 5000, "closingBalance": 2210, "totalCharges": Obj{"netTotal": 9000, "taxTotal": 450, "grossTotal": 9450}, "totalCredits": Obj{"grossTotal": 0},
		"attachments": Obj{"edges": []any{Obj{"node": Obj{"filename": "bill.pdf", "temporaryUrl": "https://example.com/bill.pdf", "temporaryUrlExpiresAt": "2026-10-15T13:00:00Z"}}}},
	}}}}}}))
	bills := env.obj("octopus_list_bills", map[string]any{"from": "2026-01-01", "limit": 3, "include_pdf_links": true})
	b := list(bills["bills"])[0].(Obj)
	if num(b["charges_gbp"]) != 94.5 || num(b["closing_balance_gbp"]) != 22.1 || get(b, "pdf", "url") != "https://example.com/bill.pdf" {
		t.Errorf("bill: %v", b)
	}
	vars := env.mock.LastGraphQL("query Bills").Variables
	if vars["from"] != "2026-01-01" || num(vars["first"]) != 3 || vars["links"] != true || vars["account"] != "A-1234ABCD" {
		t.Errorf("bills variables: %v", vars)
	}

	env.mock.OnGraphQL(`query Transactions`, octopustest.Data(Obj{"account": Obj{"transactions": Obj{"totalCount": 2, "edges": []any{
		Obj{"node": Obj{"__typename": "Payment", "id": "p1", "postedDate": "2026-10-01", "title": "Direct debit", "amounts": Obj{"net": 8000}, "balanceCarriedForward": 2210}},
		Obj{"node": Obj{"__typename": "Charge", "id": "c1", "postedDate": "2026-09-30", "title": "Electricity", "amounts": Obj{"net": 6000, "tax": 300, "gross": 6300}, "balanceCarriedForward": -5790}},
	}}}}))
	tx := env.obj("octopus_list_transactions", map[string]any{"limit": 500})
	items := list(tx["transactions"])
	if len(items) != 2 || items[0].(Obj)["type"] != "payment" || num(items[0].(Obj)["amount_gbp"]) != 80 || num(items[1].(Obj)["amount_gbp"]) != 63 {
		t.Errorf("transactions: %v", items)
	}
	if num(env.mock.LastGraphQL("query Transactions").Variables["first"]) != 99 {
		t.Error("limit should be capped below Kraken's maximum page size")
	}
}

func TestAccountDiscovery(t *testing.T) {
	env := newEnv(t)
	env.mock.OnGraphQL(`query Viewer`, octopustest.Data(Obj{"viewer": Obj{"accounts": []any{Obj{"number": "A-0000AAAA"}, Obj{"number": "A-1111BBBB"}}}}))
	env.mock.OnGraphQL(`query Balance`, octopustest.Data(Obj{"account": Obj{"number": "A-0000AAAA", "balance": 1}}))
	// An explicit account number is used as given (upper-cased).
	if bal := env.obj("octopus_get_balance", map[string]any{"account_number": "a-0000aaaa"}); bal["account_number"] != "A-0000AAAA" {
		t.Errorf("explicit account: %v", bal)
	}
	if msg := env.callErr("octopus_get_balance", map[string]any{"account_number": "12345"}); !strings.Contains(msg, "A-1234ABCD") {
		t.Errorf("bad account number: %s", msg)
	}
}
