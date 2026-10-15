# Connecting MCP clients

octopus-mcp supports both standard MCP transports:

| Transport | How it runs | Best for |
|---|---|---|
| **Streamable HTTP** (the default in the image) | One long-running container. Clients connect to `http://<host>:8080/mcp`. | Most setups. One server can be shared by several clients, sessions and machines. |
| **stdio** (the default for the binary) | The client starts the server for each session, with `docker run -i` or the local `octopus-mcp` binary. | A single machine, no network service, or clients that only support stdio. |

The HTTP endpoint is **stateless**: each request is handled on its own, so you can restart the container at any time. When `MCP_AUTH_TOKEN` is set, every request must send `Authorization: Bearer <token>`.

In the examples below, replace `<docker-host>` with the machine running the container and `<token>` with your `MCP_AUTH_TOKEN`. The server is called `octopus` in every example. If you use a different name, the prompt slash commands change to match.

- [Claude Code](#claude-code)
- [Claude Desktop](#claude-desktop)
- [VS Code (GitHub Copilot)](#vs-code-github-copilot)
- [Cursor](#cursor)
- [OpenAI Codex CLI](#openai-codex-cli)
- [Other clients](#other-clients)
- [stdio mode](#stdio-mode)

## Claude Code

```sh
claude mcp add --transport http octopus http://<docker-host>:8080/mcp \
  --header "Authorization: Bearer <token>"
```

Add `--scope user` to make it available in every project, or `--scope project` to save it in a `.mcp.json` you can share. Don't commit the token: in `.mcp.json`, reference an environment variable instead:

```json
{
  "mcpServers": {
    "octopus": {
      "type": "http",
      "url": "http://<docker-host>:8080/mcp",
      "headers": { "Authorization": "Bearer ${OCTOPUS_MCP_TOKEN}" }
    }
  }
}
```

Run `claude mcp list` to check the connection, and `/mcp` inside Claude Code to see the tools. The built-in prompts are available as slash commands, such as `/mcp__octopus__energy_report` or `/mcp__octopus__plan_cheap_usage "washing machine, 2 hours, 1 kWh"`.

To use stdio instead, with Docker or the local binary:

```sh
# Docker
claude mcp add octopus -- docker run -i --rm \
  -e MCP_TRANSPORT=stdio -e OCTOPUS_API_KEY=sk_live_... \
  ghcr.io/mattoddie/octopus-mcp:latest

# The local binary (see "Running without Docker" in the deployment guide)
claude mcp add octopus -e OCTOPUS_API_KEY=sk_live_... -- octopus-mcp
```

## Claude Desktop

Claude Desktop starts local MCP servers over stdio, so use [stdio mode](#stdio-mode). Open **Settings → Developer → Edit Config** and add:

```json
{
  "mcpServers": {
    "octopus": {
      "command": "docker",
      "args": [
        "run", "-i", "--rm",
        "-e", "MCP_TRANSPORT=stdio",
        "-e", "OCTOPUS_API_KEY", "-e", "OCTOPUS_ACCOUNT",
        "ghcr.io/mattoddie/octopus-mcp:latest"
      ],
      "env": {
        "OCTOPUS_API_KEY": "sk_live_...",
        "OCTOPUS_ACCOUNT": "A-1234ABCD"
      }
    }
  }
}
```

Or, with the local binary:

```json
{
  "mcpServers": {
    "octopus": {
      "command": "/usr/local/bin/octopus-mcp",
      "env": { "OCTOPUS_API_KEY": "sk_live_...", "OCTOPUS_ACCOUNT": "A-1234ABCD" }
    }
  }
}
```

Restart Claude Desktop. The tools appear under the tools (🔨) menu, and the prompts appear under **+ → Add from octopus**.

To turn on writes, add `"-e", "OCTOPUS_ALLOW_WRITES=true"` to `args` (or `"OCTOPUS_ALLOW_WRITES": "true"` to `env` for the binary).

## VS Code (GitHub Copilot)

Create `.vscode/mcp.json` in your workspace, or run **MCP: Add Server** from the command palette:

```json
{
  "inputs": [
    { "id": "octopus-token", "type": "promptString", "description": "octopus-mcp token", "password": true }
  ],
  "servers": {
    "octopus": {
      "type": "http",
      "url": "http://<docker-host>:8080/mcp",
      "headers": { "Authorization": "Bearer ${input:octopus-token}" }
    }
  }
}
```

VS Code asks for the token once and stores it securely. Use the tools from Copilot Chat in **Agent** mode.

## Cursor

Add to `~/.cursor/mcp.json` for all projects, or `.cursor/mcp.json` for one project:

```json
{
  "mcpServers": {
    "octopus": {
      "url": "http://<docker-host>:8080/mcp",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```

## OpenAI Codex CLI

Add to `~/.codex/config.toml`:

```toml
[mcp_servers.octopus]
url = "http://<docker-host>:8080/mcp"
bearer_token_env_var = "OCTOPUS_MCP_TOKEN"
```

Then `export OCTOPUS_MCP_TOKEN=<token>` before running `codex`.

## Other clients

Any client that supports MCP **Streamable HTTP** works. Point it at `http://<docker-host>:8080/mcp` and send the bearer token header.

If your client only supports stdio, you have two options. You can use [stdio mode](#stdio-mode), or you can keep the shared HTTP server and connect through a stdio-to-HTTP bridge such as [`mcp-remote`](https://www.npmjs.com/package/mcp-remote):

```json
{
  "command": "npx",
  "args": ["-y", "mcp-remote", "http://<docker-host>:8080/mcp", "--header", "Authorization: Bearer <token>"]
}
```

Some clients limit how many tools they accept, or slow down with a lot of tools. If yours does, narrow `OCTOPUS_TOOLSETS`. See [Configuration](configuration.md#choosing-toolsets).

## stdio mode

In stdio mode the MCP client runs the server itself and talks to it over stdin/stdout. No port is opened, and no `MCP_AUTH_TOKEN` is needed because only the client can reach the server.

With Docker, the general shape is:

```sh
docker run -i --rm \
  -e MCP_TRANSPORT=stdio \
  -e OCTOPUS_API_KEY=sk_live_... \
  ghcr.io/mattoddie/octopus-mcp:latest
```

octopus-mcp keeps nothing on disk, so there's no volume to mount and several sessions can run at once.

Tips:

- **Keep the API key out of the client config.** Pass `-e NAME` without a value so Docker copies the variable from the client's environment, as in the Claude Desktop example. You can also use `--env-file /path/to/octopus.env`.
- **Always pass `-i` and never `-t`.** A TTY corrupts the MCP message stream.
- **Docker on another machine** works too. Set `DOCKER_HOST` in the client's `env`.
- **Pin a version** (for example `:1.2.3`) so a new `latest` doesn't surprise you. See [Deployment](deployment.md#image-tags).
