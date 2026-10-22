<div align="center">

<img src="docs/assets/logo.svg" alt="octopus-mcp logo" width="112" height="112">

# octopus-mcp

**Talk to your energy.**

An open-source [Model Context Protocol](https://modelcontextprotocol.io) server for **Octopus Energy** customers in the UK. It lets Claude and other AI assistants read your smart meter usage, costs, tariffs and bills, find the cheapest Agile half-hours and, if you allow it, manage Intelligent Octopus EV charging and join Saving Sessions, all from one small Docker container.

[![CI](https://github.com/mattoddie/octopus-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/mattoddie/octopus-mcp/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/mattoddie/octopus-mcp?sort=semver)](https://github.com/mattoddie/octopus-mcp/releases)
[![Container image](https://img.shields.io/badge/ghcr.io-mattoddie%2Foctopus--mcp-2496ED?logo=docker&logoColor=white)](https://github.com/mattoddie/octopus-mcp/pkgs/container/octopus-mcp)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

**[octopus-mcp.mattoddie.dev](https://octopus-mcp.mattoddie.dev)**

[Quick start](#quick-start) · [Documentation](https://octopus-mcp.mattoddie.dev/docs/) · [Tool reference](docs/tools.md) · [Contributing](CONTRIBUTING.md)

</div>

> [!NOTE]
> octopus-mcp is an **unofficial**, independent community project. It is not affiliated with, endorsed by or supported by Octopus Energy. It uses the public customer APIs with your own API key.

---

> **"How much did I spend on electricity last week, and when should I run the dishwasher tonight?"**
> *Last week you used 58.4 kWh for £11.92, including £2.66 of standing charges, an average of 15.9p/kWh on Agile. Wednesday was the priciest day: you used 4.1 kWh between 4pm and 7pm, when prices were over 30p. Tonight the cheapest two hours are 01:30 to 03:30 at an average of 7.8p/kWh, so a 1.5 kWh dishwasher cycle would cost about 12p, against 46p if you started it now.*

Ask about your energy in plain English. The assistant picks from **22 tools** to look things up and, if you allow it, make changes. Some examples:

- 📊 *"Give me a report on my energy use for September."*
- ⚡ *"What are the Agile prices for tomorrow? Any negative ones?"*
- 🕑 *"Find the cheapest 3 hours overnight to charge the home battery."*
- 🔥 *"How much gas did we use this month, in kWh?"*
- 🚗 *"When will the car charge tonight? Make it 90% by 7am tomorrow."*
- 🎁 *"Is there a Saving Session coming up? Sign me up."*

## Highlights

- **🐳 Docker-first and tiny.** One image (`ghcr.io/mattoddie/octopus-mcp`) for `linux/amd64` and `linux/arm64`, about 20 MB. It's a single static Go binary on a distroless base, with no shell or package manager, running as a non-root user with nothing to write. It serves Streamable HTTP for a long-running service, or stdio for `docker run -i`. You can also run the binary on its own, and for Claude Desktop there's a one-click extension bundle (`.mcpb`) that needs no Docker.
- **💷 Real costs, half-hour by half-hour.** Consumption is priced with the actual unit rate for every half-hour from the tariff you were on at the time, plus standing charges, so Agile, Go, Intelligent Go, Cosy, Flux and Tracker are costed properly.
- **🕑 Cheapest-time finder.** Finds the cheapest continuous block for a job, runner-up times and the cheapest half-hours, and says what it'll cost against running it now.
- **🔥 Honest about units.** SMETS2 gas meters report m³. octopus-mcp knows which your meter uses, converts with the standard formula, and tells you what it assumed.
- **🛡️ Safe by default.** The server is read-only unless you turn on writes. Tools that aren't allowed aren't registered at all, and the raw GraphQL tool can't run mutations without writes, or ever touch tokens or API keys.
- **🔑 Simple, secret-safe auth.** Just your Octopus API key: REST requests use it directly, and GraphQL gets short-lived Kraken tokens that are renewed automatically and kept in memory. Neither the key nor tokens ever appear in output or logs.
- **🎛️ Tunable.** Expose only the toolsets you need, which keeps the model's context small and focused.
- **🧪 Tested.** The test suite runs against a mock Octopus REST API and GraphQL APIs on every build.

## Quick start

You need Docker, an Octopus Energy account (with a smart meter for usage data) and an MCP client such as [Claude Code](https://claude.com/claude-code).

**1. Get your API key** from **[Personal details → API access](https://octopus.energy/dashboard/new/accounts/personal-details/api-access)** in your Octopus account. Note your account number (`A-1234ABCD`) too.

**2. Set up and start the server.**

```sh
mkdir octopus-mcp && cd octopus-mcp
curl -fsSLO https://raw.githubusercontent.com/mattoddie/octopus-mcp/main/compose.yaml
curl -fsSL https://raw.githubusercontent.com/mattoddie/octopus-mcp/main/example.env -o .env
# Edit .env: set OCTOPUS_API_KEY, OCTOPUS_ACCOUNT
# and a random MCP_AUTH_TOKEN (e.g. `openssl rand -hex 32`)
docker compose up -d
docker compose exec octopus-mcp /octopus-mcp status   # checks the API key
```

**3. Connect your assistant.**

```sh
claude mcp add --transport http octopus http://<docker-host>:8080/mcp \
  --header "Authorization: Bearer <MCP_AUTH_TOKEN>"
```

Now ask *"What tariff am I on, and what's my electricity price right now?"* in Claude Code.

**Using Claude Desktop on a Mac or Windows PC?** You don't need Docker at all. Download the `.mcpb` bundle for your computer from the [latest release](https://github.com/mattoddie/octopus-mcp/releases/latest), open it, and enter your API key in the settings. See [Claude Desktop](docs/clients.md#claude-desktop).

Using **VS Code, Cursor or Codex**? See [Connecting MCP clients](docs/clients.md). Prefer not to run a service? Use [stdio mode](docs/clients.md#stdio-mode) with `docker run -i` or the local binary.

## Turning on changes

Out of the box the assistant can look but not touch. To let it change Intelligent Octopus charging settings, start a bump charge and join Saving Sessions, add this to `.env` and restart:

```sh
OCTOPUS_ALLOW_WRITES=true
OCTOPUS_TOOLSETS=account,consumption,tariffs,intelligent,octoplus   # optional: expose fewer tools
```

Octopus API keys can't be limited to read-only, so this switch, enforced by octopus-mcp, is what keeps the assistant read-only. Read [Security](docs/security.md) before turning it on. To keep a record of every change the assistant makes, set `OCTOPUS_AUDIT_LOG` to a file path (see [Audit log](docs/configuration.md#audit-log)).

## What it can do

| Toolset | What's covered |
|---|---|
| `account` | The account overview (properties, MPAN/MPRN, meter serials, smart meter device IDs, current tariffs), balance, bills and transactions |
| `consumption` | Smart meter usage for electricity import and export and gas, by half-hour, hour, day, week or month, with cost estimates; live demand from a Home Mini |
| `tariffs` | Products on sale, prices by region, half-hourly unit rates (Agile, Go and more), standing charges, the cheapest times to use electricity, region by postcode. No account needed |
| `intelligent` | Intelligent Octopus cars and chargers: status, schedule, planned and completed charging slots; with writes, target and ready-by time, bump charge, smart control |
| `octoplus` | Octoplus points, Saving Sessions with rewards and results; with writes, joining a session |
| `raw` | Escape hatches: any GraphQL query (mutations only with writes) and any REST GET |

See the **[tool reference](docs/tools.md)** for every tool and argument. Ready-made prompts like `energy_report`, `plan_cheap_usage` and `compare_tariffs` chain the tools together for common jobs.

## Documentation

The docs are also published at **[octopus-mcp.mattoddie.dev/docs](https://octopus-mcp.mattoddie.dev/docs/)**, with search.

| | |
|---|---|
| 🚀 [Getting started](docs/getting-started.md) | Step-by-step setup, from API key to first question |
| 🔌 [Connecting MCP clients](docs/clients.md) | Claude Code, Claude Desktop, VS Code, Cursor, Codex, stdio mode |
| ⚙️ [Configuration](docs/configuration.md) | Every environment variable, how authentication works, Docker secrets, toolsets |
| 🐳 [Deployment](docs/deployment.md) | Compose, image tags, upgrades, TLS reverse proxy, hardening, running without Docker |
| 💬 [Using octopus-mcp](docs/usage.md) | Example conversations, prompts, units, cost estimates, gas in m³, EV charging, Saving Sessions |
| 🧰 [Tool reference](docs/tools.md) | Every tool and prompt, generated from the code |
| 🛡️ [Security](docs/security.md) | Threat model, the API key, safe write setups |
| 📡 [Compatibility](docs/compatibility.md) | The Octopus APIs, meters and tariffs, and known gaps |
| 🩹 [Troubleshooting](docs/troubleshooting.md) | Common errors and how to fix them |
| 🛠️ [Development](docs/development.md) | Architecture, adding a tool, testing |

## Contributing

Contributions are very welcome, whether that's bug reports, docs or new tools. Read the [contributing guide](CONTRIBUTING.md) to get started, and please follow the [code of conduct](CODE_OF_CONDUCT.md). To report a vulnerability, see the [security policy](SECURITY.md). Please don't open a public issue for one.

## License

[MIT](LICENSE) © Matt Oddie

*octopus-mcp is an independent community project. It is not affiliated with, endorsed by or sponsored by Octopus Energy Ltd or Kraken Technologies Ltd. "Octopus Energy", "Agile Octopus", "Intelligent Octopus", "Octoplus" and related names are trademarks of their owners and are used here only to describe what the software works with.*
