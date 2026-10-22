# Security

octopus-mcp gives an AI assistant access to your Octopus Energy account. This page explains what that means and how to set it up safely. To report a vulnerability, see the [security policy](../SECURITY.md).

- [Threat model](#threat-model)
- [Layers of protection](#layers-of-protection)
- [The API key](#the-api-key)
- [Recommended setups](#recommended-setups)
- [Network exposure](#network-exposure)
- [Checklist](#checklist)

## Threat model

Four things need protecting:

1. **The MCP endpoint.** Anyone who can send requests to `/mcp` can use every registered tool with your API key. That includes reading your address, meter details, half-hourly usage (which shows when you're home or away), balance and bills, and, if you've allowed it, changing your EV charging.
2. **The API key.** An Octopus API key isn't scoped: it can read everything on your Octopus login, across all its accounts, and make the same changes through GraphQL that octopus-mcp can, and some it can't. It doesn't expire until you regenerate it.
3. **The assistant's judgement.** A model can misunderstand a request, or be steered by malicious text it reads (*prompt injection*), for example from a web page or document pasted into the same conversation. octopus-mcp's data mostly comes from Octopus, but tariff descriptions, device names and similar text are still passed to the model.
4. **Your data, on its way to the model.** Whatever the tools return, such as your address, usage patterns and bills, is sent to your AI provider as part of the conversation. Check that you're comfortable with your provider's data handling.

The best defence is to limit what's possible, not to rely on the model behaving.

## Layers of protection

| Layer | Setting | What it does |
|---|---|---|
| Write switch | `OCTOPUS_ALLOW_WRITES` (off by default) | When off, no tools that change anything are registered, and the raw GraphQL tool only sends documents whose every operation is a query. It reads documents with a GraphQL lexer that follows the specification (commas, a byte order mark and comments are ignored as GraphQL ignores them), and refuses anything it can't read. Subscriptions are always refused. |
| Toolsets | `OCTOPUS_TOOLSETS` | Tools outside the listed toolsets don't exist. The model can't see or call them. |
| Secret-handling operations | Built in | The raw GraphQL tool always refuses a document that names anything to do with tokens, API keys or passwords (such as `obtainKrakenToken` or `regenerateSecretKey`), whether writes are on or off. |
| Tool annotations | Built in | Each tool is marked read-only or not. Bump charges and suspending smart charging are marked open-world because they affect a real car, so MCP clients can ask for your confirmation. |
| Audit log | `OCTOPUS_AUDIT_LOG` (off by default) | Appends a JSON line for every call of a tool that changes things, and every raw GraphQL mutation, with secret-looking arguments redacted. It records what happened; it doesn't prevent anything. See [Audit log](configuration.md#audit-log). |
| Server instructions | Built in | With writes on, the model is told to confirm EV changes and joining Saving Sessions with you first. This helps, but it isn't a security boundary. |
| No secret output | Built in | No tool returns the API key or a Kraken token. Error messages, including the details Octopus adds to GraphQL errors, are scrubbed of the key and of current and recent tokens. Public tariff requests don't send the key at all. |
| No redirects, https only | Built in | Requests to Octopus never follow redirects, so the key and tokens only go to the configured URLs, and paged results are always fetched from the configured host. The API URLs must be `https://` (plain `http://` only for localhost) and can't contain a user name or password. A POST that Octopus answers with 503 isn't retried, so a mutation is never sent twice. |
| Endpoint auth | `MCP_AUTH_TOKEN` | Bearer token required on every request, checked in constant time. The http transport refuses to start without one, or with one shorter than 16 characters. |
| Host and Origin allow-list | `MCP_ALLOWED_HOSTS` | Rejects requests with an unexpected `Host` header, or with an `Origin` header (sent by browsers) that isn't on the list. This blocks DNS-rebinding and cross-site attacks from browser pages. Required when running without a token. `/mcp` only accepts `POST` with a JSON body of up to 4 MiB. |
| Container | Built in | A distroless image with no shell, running as a non-root user with nothing to write. The default `compose.yaml` uses a read-only root filesystem, `cap_drop: [ALL]` and `no-new-privileges`. |

### Why the switch matters more than usual

Many APIs let you create a read-only key. Octopus doesn't: the API key can do everything your login can through the API. So `OCTOPUS_ALLOW_WRITES` is enforced by octopus-mcp, not by Octopus. It still means a lot, because the model can only call tools that exist, but a bug in octopus-mcp, or a raw GraphQL query if you enable `raw`, is the only thing between the model and those changes. Keep `raw` out of `OCTOPUS_TOOLSETS` unless you need it.

## The API key

- Keep it in `.env` (not committed; `.env` is in `.gitignore` and `.dockerignore`) or, better, a Docker secret with `OCTOPUS_API_KEY_FILE`.
- Don't paste it into a chat with the assistant. octopus-mcp never needs it from the model.
- The key is sent to `api.octopus.energy` only: as HTTP Basic auth for account REST requests, and to `obtainKrakenToken` to get a token. Kraken tokens are kept in memory and last about an hour.
- If you think the key has leaked, regenerate it on the [API access page](https://octopus.energy/dashboard/new/accounts/personal-details/api-access). The old key stops working immediately. Update `.env` and restart.

## Recommended setups

**Read-only** (lowest risk, the default):

- Writes off
- Leave out `raw` if you don't need it
- Set `MCP_AUTH_TOKEN`, even on a home network

**EV charging or Saving Sessions:**

- `OCTOPUS_ALLOW_WRITES=true`
- Only the toolsets you use, for example `account,tariffs,intelligent`, and leave `raw` out
- Keep tool confirmations on in your MCP client, so you approve each change
- Remember that a bump charge usually costs the peak rate

## Network exposure

- Set `MCP_AUTH_TOKEN` to a long random value.
- Publish the port only where it's needed: on a LAN address, or on `127.0.0.1` behind a reverse proxy.
- **Never expose port 8080 directly to the internet.** If traffic crosses a network you don't trust, put TLS in front of it. See [Deployment](deployment.md#tls-with-a-reverse-proxy). Prefer a VPN to a public endpoint.
- Set `MCP_ALLOWED_HOSTS` to the hostname clients use.
- The server only needs outbound HTTPS to `api.octopus.energy` and `api.backend.octopus.energy`. If your firewall allows, restrict it to those.

## Checklist

- [ ] `MCP_AUTH_TOKEN` is set to a long random value (`openssl rand -hex 32`).
- [ ] The port is only reachable from machines that need it, or published on `127.0.0.1` behind a proxy.
- [ ] TLS terminates at a reverse proxy if traffic crosses an untrusted network. Prefer a VPN to a public endpoint.
- [ ] Writes are only enabled if you need them.
- [ ] `OCTOPUS_TOOLSETS` lists only the toolsets you use, and `raw` is left out unless needed.
- [ ] The API key is in a secret or an `.env` file that isn't committed or shared.
- [ ] The image is pinned to a version, and you update when security releases come out (watch the repository's releases).
