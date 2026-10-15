package octopus_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
	"github.com/mattoddie/octopus-mcp/internal/octopus/octopustest"
)

func newClient(t *testing.T) (*octopustest.Mock, *octopus.Client) {
	t.Helper()
	m := octopustest.New()
	t.Cleanup(m.Server.Close)
	return m, octopus.New(m.Config())
}

func TestKrakenTokenIsReusedAndRefreshed(t *testing.T) {
	m, c := newClient(t)
	m.OnGraphQL(`query Viewer`, octopustest.Data(map[string]any{"viewer": map[string]any{"accounts": []any{map[string]any{"number": "A-1234ABCD"}}}}))
	ctx := context.Background()
	var out map[string]any
	for i := 0; i < 3; i++ {
		if err := c.GraphQL(ctx, `query Viewer { viewer { accounts { number } } }`, nil, &out); err != nil {
			t.Fatal(err)
		}
	}
	if m.TokenRequests != 1 || m.TokenInputs[0]["APIKey"] != octopustest.APIKey {
		t.Errorf("token requests: %d %v", m.TokenRequests, m.TokenInputs)
	}
	if r := m.LastGraphQL("Viewer"); !strings.HasPrefix(r.Token, "eyJ") {
		t.Errorf("the raw token goes in the Authorization header: %q", r.Token)
	}
	// Kraken rejects the token (KT-CT-1124): the client gets a new one with the refresh token and retries once.
	m.ExpireTokens()
	if err := c.GraphQL(ctx, `query Viewer { viewer { accounts { number } } }`, nil, &out); err != nil {
		t.Fatal(err)
	}
	if m.TokenRequests != 2 || m.TokenInputs[1]["refreshToken"] != "refresh-1" {
		t.Errorf("refresh: %d %v", m.TokenRequests, m.TokenInputs)
	}
	// A token close to expiry is replaced before use.
	c.Now = func() time.Time { return time.Now().Add(59 * time.Minute) }
	if _, err := c.Token(ctx, false); err != nil {
		t.Fatal(err)
	}
	if m.TokenRequests != 3 {
		t.Errorf("expiring token not renewed: %d", m.TokenRequests)
	}
	if st := c.TokenStatus(); st["has_token"] != true || strings.Contains(fmt.Sprint(st), "eyJ") {
		t.Errorf("status must not show the token: %v", st)
	}
}

func TestBadAPIKeyNeverLeaks(t *testing.T) {
	m := octopustest.New()
	defer m.Server.Close()
	cfg := m.Config()
	cfg.APIKey = "sk_live_wrong_key_123456"
	c := octopus.New(cfg)
	err := c.GraphQL(context.Background(), `query { viewer { email } }`, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "OCTOPUS_API_KEY") || !strings.Contains(err.Error(), "KT-CT-1135") {
		t.Fatalf("error: %v", err)
	}
	if strings.Contains(err.Error(), cfg.APIKey) {
		t.Errorf("error leaks the key: %v", err)
	}
	// REST: 401 with a hint, and no key in the message.
	err = c.Get(context.Background(), "/accounts/A-1234ABCD/", nil, nil)
	var apiErr *octopus.Error
	if !errors.As(err, &apiErr) || apiErr.Status != 401 || !strings.Contains(err.Error(), "API access") || strings.Contains(err.Error(), cfg.APIKey) {
		t.Errorf("rest 401: %v", err)
	}
}

func TestRESTAuthPagingAndErrors(t *testing.T) {
	m, c := newClient(t)
	ctx := context.Background()
	m.On("GET", "/accounts/A-1234ABCD/", octopustest.JSON(map[string]any{"number": "A-1234ABCD"}))
	var acct map[string]any
	if err := c.Get(ctx, "accounts/A-1234ABCD", nil, &acct); err != nil || acct["number"] != "A-1234ABCD" {
		t.Fatalf("account: %v %v", acct, err)
	}
	if r := m.Last("/accounts/"); r.BasicUser != octopustest.APIKey || r.Path != "/accounts/A-1234ABCD/" {
		t.Errorf("trailing slash and basic auth: %+v", r)
	}
	m.On("GET", "/products/", func(r octopustest.Request, _ []string) (int, any) {
		if r.Query.Get("page") == "2" {
			return 200, map[string]any{"count": 3, "next": nil, "results": []any{map[string]any{"code": "C"}}}
		}
		// A next link on another host is followed on the configured host only.
		return 200, map[string]any{"count": 3, "next": "https://elsewhere.example/v1/products/?page=2", "results": []any{map[string]any{"code": "A"}, map[string]any{"code": "B"}}}
	})
	page, err := c.GetPages(ctx, "/products/", nil, false, 100)
	if err != nil || len(page.Results) != 3 || page.Count != 3 || page.Truncated {
		t.Fatalf("pages: %+v %v", page, err)
	}
	if m.Last("/products/").BasicUser != "" {
		t.Error("public endpoints shouldn't get the API key")
	}
	page, _ = c.GetPages(ctx, "/products/", nil, false, 2)
	if len(page.Results) != 2 || !page.Truncated {
		t.Errorf("max items: %+v", page)
	}

	err = c.GetPublic(ctx, "/products/NOPE/", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") || !strings.Contains(err.Error(), "Not found") || !strings.Contains(err.Error(), "octopus_list_products") {
		t.Errorf("404: %v", err)
	}
}

func TestRetriesWhenRateLimited(t *testing.T) {
	m, c := newClient(t)
	calls := 0
	m.On("GET", "/products/", func(octopustest.Request, []string) (int, any) {
		calls++
		if calls < 2 {
			return 429, map[string]any{"detail": "Request was throttled."}
		}
		return 200, map[string]any{"results": []any{}}
	})
	if err := c.GetPublic(context.Background(), "/products/", nil, nil); err != nil || calls != 2 {
		t.Errorf("retry: %v after %d calls", err, calls)
	}
}

func TestAccountDiscovery(t *testing.T) {
	m, c := newClient(t)
	ctx := context.Background()
	m.OnGraphQL(`query Viewer`, octopustest.Data(map[string]any{"viewer": map[string]any{"accounts": []any{map[string]any{"number": "A-1111BBBB"}}}}))
	if a, err := c.Account(ctx, ""); err != nil || a != "A-1111BBBB" {
		t.Errorf("one account: %s %v", a, err)
	}
	m2, c2 := newClient(t)
	m2.OnGraphQL(`query Viewer`, octopustest.Data(map[string]any{"viewer": map[string]any{"accounts": []any{map[string]any{"number": "A-2222CCCC"}, map[string]any{"number": "A-1111BBBB"}}}}))
	if _, err := c2.Account(ctx, ""); err == nil || !strings.Contains(err.Error(), "A-1111BBBB, A-2222CCCC") {
		t.Errorf("several accounts: %v", err)
	}
	if a, _ := c2.Account(ctx, "a-2222cccc"); a != "A-2222CCCC" {
		t.Errorf("explicit: %s", a)
	}
}

func TestGraphQLErrorsKeepPartialData(t *testing.T) {
	m, c := newClient(t)
	m.OnGraphQL(`query Both`, func(octopustest.GraphQLRequest) (any, []any) {
		return map[string]any{"a": 1, "b": nil}, []any{map[string]any{"message": "Unable to fetch planned dispatches.", "extensions": map[string]any{"errorCode": "KT-CT-4340"}}}
	})
	var out map[string]any
	err := c.GraphQL(context.Background(), `query Both { a b }`, nil, &out)
	var gerr octopus.GraphQLErrors
	if !errors.As(err, &gerr) || gerr[0].Code() != "KT-CT-4340" || out["a"] == nil {
		t.Errorf("partial: %v %v", out, err)
	}
	if m.TokenRequests != 1 {
		t.Error("non-auth errors must not trigger a token refresh")
	}
}

func TestRatesLookup(t *testing.T) {
	m, c := newClient(t)
	base := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	m.On("GET", "/products/GO-VAR-22-10-14/electricity-tariffs/E-1R-GO-VAR-22-10-14-C/standard-unit-rates/", octopustest.JSON(map[string]any{"count": 3, "results": []any{
		map[string]any{"value_inc_vat": 30.0, "value_exc_vat": 28.57, "valid_from": base.Add(4*time.Hour + 30*time.Minute).Format(time.RFC3339), "valid_to": nil, "payment_method": "DIRECT_DEBIT"},
		map[string]any{"value_inc_vat": 32.0, "value_exc_vat": 30.48, "valid_from": base.Add(4*time.Hour + 30*time.Minute).Format(time.RFC3339), "valid_to": nil, "payment_method": "NON_DIRECT_DEBIT"},
		map[string]any{"value_inc_vat": 8.5, "value_exc_vat": 8.1, "valid_from": base.Add(-30 * time.Minute).Format(time.RFC3339), "valid_to": base.Add(4*time.Hour + 30*time.Minute).Format(time.RFC3339), "payment_method": "DIRECT_DEBIT"},
	}}))
	tr, _ := octopus.ParseTariff("E-1R-GO-VAR-22-10-14-C")
	rs, err := c.TariffRates(context.Background(), tr, "standard-unit-rates", base, base.Add(24*time.Hour))
	if err != nil || len(rs) != 2 {
		t.Fatalf("direct debit rates only, oldest first: %v %v", rs, err)
	}
	if r, ok := rs.At(base.Add(2 * time.Hour)); !ok || r.IncVAT != 8.5 {
		t.Errorf("off-peak: %v", r)
	}
	if r, ok := rs.At(base.Add(12 * time.Hour)); !ok || r.IncVAT != 30 {
		t.Errorf("open-ended peak: %v", r)
	}
	if _, ok := rs.At(base.Add(-2 * time.Hour)); ok {
		t.Error("no rate before the first record")
	}
	q := m.Last("/standard-unit-rates/").Query
	if q.Get("period_from") != "2026-10-15T00:00:00Z" || q.Get("page_size") != "1500" {
		t.Errorf("rates query: %v", q)
	}
	if !octopus.IsNight(time.Date(2026, 7, 1, 2, 0, 0, 0, octopus.London)) || octopus.IsNight(time.Date(2026, 7, 1, 8, 30, 0, 0, octopus.London)) {
		t.Error("Economy 7 night hours are in GMT")
	}
}
