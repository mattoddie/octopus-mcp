package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/octopus-mcp/internal/config"
	"github.com/mattoddie/octopus-mcp/internal/octopus"
)

// Name is the MCP server name.
const Name = "octopus-mcp"

// Version is set at build time with -ldflags "-X github.com/mattoddie/octopus-mcp/internal/tools.Version=1.2.3".
var Version = "0.0.0-dev"

func instructions(opts config.Tools) string {
	s := "Tools for an Octopus Energy (UK energy supplier) household account: the account's properties, meters and tariffs, smart meter consumption with cost estimates, unit rates and standing charges (including half-hourly Agile prices), bills and balance, Intelligent Octopus EV charging, and Octoplus Saving Sessions. " +
		"Start with octopus_get_account for the meters and current tariffs. Prices are in pence including VAT unless a field says otherwise; money totals are in pounds. Times are UK local time; dates are whole UK days. " +
		"Electricity is in kWh; SMETS2 gas meters report m³, which tools convert to kWh where they estimate costs, saying so. Cost estimates come from published rates and won't exactly match bills. "
	if opts.AllowWrites {
		s += "Write tools are enabled: confirm with the user before changing EV charging settings, starting a bump charge or joining a Saving Session. "
	} else {
		s += "The server is read-only; write tools are disabled (OCTOPUS_ALLOW_WRITES=false). "
	}
	return s
}

// NewServer creates the MCP server with the tools and prompts allowed by opts.
// registered, when non-nil, is filled with each tool's toolset and gate.
func NewServer(client *octopus.Client, opts config.Tools, registered map[string]Info) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: Name, Version: Version}, &mcp.ServerOptions{Instructions: instructions(opts)})
	c := &Context{Server: s, Client: client, Tools: opts, Registered: registered, audit: newAuditLog(opts.AuditLog)}
	registerAccount(c)
	registerConsumption(c)
	registerTariffs(c)
	registerIntelligent(c)
	registerOctoplus(c)
	registerRaw(c)
	registerPrompts(s)
	return s
}
