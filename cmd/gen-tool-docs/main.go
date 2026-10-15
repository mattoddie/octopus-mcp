// Command gen-tool-docs writes docs/tools.md from the registered tools and
// prompts, so the reference can't drift from the code. With -check it fails
// when the committed file is out of date.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/octopus-mcp/internal/config"
	"github.com/mattoddie/octopus-mcp/internal/octopus"
	"github.com/mattoddie/octopus-mcp/internal/tools"
)

var toolsetInfo = map[string]string{
	"account":     "The account overview (properties, meter points with MPAN/MPRN and serial numbers, current tariffs), the balance, bills and transactions.",
	"consumption": "Smart meter consumption for electricity import and export and gas, totalled by half-hour, hour, day, week or month with cost estimates, and live demand from a Home Mini.",
	"tariffs":     "Public tariff data: products on sale, prices by region, half-hourly unit rates (Agile, Go and others), standing charges, the cheapest times to use electricity, and the region for a postcode.",
	"intelligent": "Intelligent Octopus: connected cars and chargers, planned and completed charging slots, and (with writes) the charging target and ready-by time, bump charges and smart control.",
	"octoplus":    "Octoplus rewards: points balance, Saving Sessions with their rewards and results, and (with writes) joining a session.",
	"raw":         "Raw API escape hatches for anything the other tools don't cover: GraphQL queries (mutations only with writes enabled) and REST GET requests.",
}

var badge = map[tools.Gate]string{tools.Read: "🟢 read", tools.Write: "🟠 write"}

type schema struct {
	Type        any                `json:"type"`
	Description string             `json:"description"`
	Enum        []any              `json:"enum"`
	Items       *schema            `json:"items"`
	Properties  map[string]*schema `json:"properties"`
	Required    []string           `json:"required"`
}

func typeOf(s *schema) string {
	if s == nil {
		return "any"
	}
	if len(s.Enum) > 0 {
		var parts []string
		for _, v := range s.Enum {
			b, _ := json.Marshal(v)
			parts = append(parts, "`"+string(b)+"`")
		}
		return strings.Join(parts, " | ")
	}
	switch t := s.Type.(type) {
	case string:
		if t == "array" {
			return typeOf(s.Items) + "[]"
		}
		return t
	case []any:
		var parts []string
		for _, x := range t {
			parts = append(parts, fmt.Sprint(x))
		}
		return strings.Join(parts, " | ")
	}
	return "any"
}

// rows renders arguments, required first, with the fields of nested objects (one level) as name.field rows.
func rows(prefix string, s *schema) []string {
	required := map[string]bool{}
	for _, r := range s.Required {
		required[r] = true
	}
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if required[names[i]] != required[names[j]] {
			return required[names[i]]
		}
		return names[i] < names[j]
	})
	var out []string
	for _, n := range names {
		p := s.Properties[n]
		req := ""
		if required[n] {
			req = "yes"
		}
		out = append(out, fmt.Sprintf("| `%s%s` | %s | %s | %s |", prefix, n, cell(typeOf(p)), req, cell(p.Description)))
		if prefix != "" {
			continue
		}
		switch {
		case p.Items != nil && len(p.Items.Properties) > 0:
			out = append(out, rows(n+"[].", p.Items)...)
		case len(p.Properties) > 0:
			out = append(out, rows(n+".", p)...)
		}
	}
	return out
}

var spaces = regexp.MustCompile(`\s*\n\s*`)

func cell(s string) string { return spaces.ReplaceAllString(strings.ReplaceAll(s, "|", "\\|"), " ") }

func main() {
	check := flag.Bool("check", false, "fail if docs/tools.md is out of date instead of writing it")
	out := flag.String("o", "docs/tools.md", "output file")
	flag.Parse()

	// A client that is never used: listing tools doesn't touch Octopus.
	client := octopus.New(config.Octopus{APIKey: "unused", APIURL: "https://invalid", GraphQLURL: "https://invalid/", BackendGraphQLURL: "https://invalid/", Timeout: 1e9})
	registered := map[string]tools.Info{}
	server := tools.NewServer(client, config.AllTools(), registered)
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		log.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "gen-tool-docs", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	var all []*mcp.Tool
	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			log.Fatal(err)
		}
		all = append(all, t)
	}
	var prompts []*mcp.Prompt
	for p, err := range session.Prompts(ctx, nil) {
		if err != nil {
			log.Fatal(err)
		}
		prompts = append(prompts, p)
	}
	counts := map[tools.Gate]int{}
	for _, t := range all {
		counts[registered[t.Name].Gate]++
	}

	var b strings.Builder
	w := func(lines ...string) {
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
	}
	w("# Tool reference", "",
		"<!-- Generated by `make docs` (go run ./cmd/gen-tool-docs) from the tool definitions. Do not edit by hand. -->", "",
		fmt.Sprintf("octopus-mcp has **%d tools** in **%d toolsets**: %d read and %d write. It also has **%d prompts**.",
			len(all), len(config.Toolsets), counts[tools.Read], counts[tools.Write], len(prompts)), "",
		"Each tool is labelled with the setting it needs:", "",
		"| Label | Registered when |", "|---|---|",
		"| 🟢 read | Always |",
		"| 🟠 write | `OCTOPUS_ALLOW_WRITES=true` |", "",
		"Every tool also needs its toolset to be enabled in `OCTOPUS_TOOLSETS`, which enables all toolsets by default. Prices are in pence per kWh (unit rates) or pence per day (standing charges), including VAT; money totals are in pounds. Times are UK local time. Time arguments take `YYYY-MM-DD` (a whole UK day), a date and time, or `now`, `today`, `yesterday` and `tomorrow`.", "",
		"## Contents", "")
	for _, ts := range config.Toolsets {
		w(fmt.Sprintf("- [`%s`](#%s): %s", ts, ts, toolsetInfo[ts]))
	}
	w("- [Prompts](#prompts)", "")

	for _, ts := range config.Toolsets {
		w("## "+ts, "", toolsetInfo[ts], "", "| Tool | Access | Summary |", "|---|---|---|")
		var inSet []*mcp.Tool
		for _, t := range all {
			if registered[t.Name].Toolset == ts {
				inSet = append(inSet, t)
			}
		}
		for _, t := range inSet {
			w(fmt.Sprintf("| [`%s`](#%s) | %s | %s |", t.Name, t.Name, badge[registered[t.Name].Gate], cell(t.Title)))
		}
		w("")
		for _, t := range inSet {
			label := badge[registered[t.Name].Gate]
			if a := t.Annotations; a != nil && !a.ReadOnlyHint && a.DestructiveHint != nil && *a.DestructiveHint {
				label += " · destructive"
			}
			w("### "+t.Name, "", label, "", t.Description, "")
			raw, _ := json.Marshal(t.InputSchema)
			var s schema
			_ = json.Unmarshal(raw, &s)
			if len(s.Properties) == 0 {
				w("_No arguments._", "")
				continue
			}
			w("| Argument | Type | Required | Description |", "|---|---|---|---|")
			for _, row := range rows("", &s) {
				w(row)
			}
			w("")
		}
	}

	w("## Prompts", "", "Prompts are ready-made requests that tell the model which tools to call and what to report. In Claude Code they appear as slash commands, such as `/mcp__octopus__energy_report`.", "")
	for _, p := range prompts {
		w("### "+p.Name, "", p.Description, "")
		if len(p.Arguments) > 0 {
			w("| Argument | Required | Description |", "|---|---|---|")
			for _, a := range p.Arguments {
				req := ""
				if a.Required {
					req = "yes"
				}
				w(fmt.Sprintf("| `%s` | %s | %s |", a.Name, req, cell(a.Description)))
			}
			w("")
		}
	}

	text := regexp.MustCompile(`\n{3,}`).ReplaceAllString(b.String(), "\n\n")
	text = strings.TrimRight(text, "\n") + "\n"
	if *check {
		current, _ := os.ReadFile(*out)
		if string(current) != text {
			log.Fatalf("%s is out of date. Run `make docs` and commit the result.", *out)
		}
		fmt.Printf("%s is up to date.\n", *out)
		return
	}
	if err := os.WriteFile(*out, []byte(text), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Wrote %s (%d tools, %d prompts).\n", *out, len(all), len(prompts))
}
