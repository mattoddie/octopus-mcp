# Security policy

octopus-mcp has access to your energy account, home address, usage patterns and, with writes on, EV charging, so security reports are taken seriously and handled as a priority.

## Supported versions

Security fixes go into the latest release. Please upgrade to the newest version before reporting.

| Version | Supported |
|---|---|
| Latest release | ✅ |
| Older releases | ❌ |

## Reporting a vulnerability

**Please don't report security vulnerabilities in public issues, discussions or pull requests.**

Report them privately through GitHub's **[private vulnerability reporting](https://github.com/mattoddie/octopus-mcp/security/advisories/new)**: go to the repository's **Security** tab and click **Report a vulnerability**.

Please include:

- A description of the issue and its impact
- Steps to reproduce, or a proof of concept
- The affected version(s) and configuration, such as transport, whether writes are on, and toolsets
- Any suggested fix, if you have one

Please don't include your API key, account number, MPAN/MPRN or real usage data.

## What to expect

- An acknowledgement within **3 working days**.
- An initial assessment within **7 days**, including whether the report is accepted and a rough timeline.
- A fix released as soon as practical, with a GitHub security advisory and credit to you, unless you'd rather stay anonymous.

Please give us a reasonable amount of time to fix the issue before you disclose it publicly.

## Scope

In scope:

- Bypassing `MCP_AUTH_TOKEN`, `MCP_ALLOWED_HOSTS`, or the write or toolset gating. Examples would be running a GraphQL mutation through `octopus_graphql` when writes are disabled, or obtaining a token or API key through it at all.
- Leaking the API key or Kraken tokens through tool output, logs or error messages, or sending them anywhere other than the configured Octopus API
- Sending the API key to public endpoints, or following redirects or paging links that carry it to another host
- Vulnerabilities in the Docker image or the release pipeline

Out of scope:

- Issues that need an attacker who already holds a valid `MCP_AUTH_TOKEN` and uses only the tools that configuration allows. That's intended behaviour.
- Prompt injection that leads the model to call tools the configuration allows, for example through a web page or document pasted into the chat. This is a known limitation of AI assistants. [docs/security.md](docs/security.md) explains how to limit the impact.
- Anything an Octopus API key can do directly. Octopus keys aren't scoped, and protecting the key is the operator's job.
- Vulnerabilities in Octopus Energy's or Kraken's systems or APIs. Report those to Octopus Energy directly.

For guidance on deploying octopus-mcp securely, see [docs/security.md](docs/security.md).
