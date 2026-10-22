# Connecting MCP clients

octopus-mcp supports both standard MCP transports:

| Transport | How it runs | Best for |
|---|---|---|
| **Streamable HTTP** (the default in the image) | One long-running container. Clients connect to `http://<host>:8080/mcp`. | Most setups. One server can be shared by several clients, sessions and machines. |
| **stdio** (the default for the binary) | The client starts the server for each session, with `docker run -i` or the local `octopus-mcp` binary. | A single machine, no network service, or clients that only support stdio. |

The HTTP endpoint is **stateless**: each request is handled on its own, so you can restart the container at any time. When `MCP_AUTH_TOKEN` is set, every request must send `Authorization: Bearer <token>`.

On **Claude Desktop** you can skip Docker altogether: install the [extension bundle](#claude-desktop), which runs the server for you over stdio.

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

The easiest way is the **extension bundle**: a single `.mcpb` file that installs octopus-mcp into Claude Desktop with a settings form. It needs no Docker, no service and no token, because Claude Desktop runs the server itself over stdio.

### Install the extension

1. Open the [latest release](https://github.com/mattoddie/octopus-mcp/releases/latest) and download the bundle for your computer:

   | Computer | Bundle |
   |---|---|
   | Mac with Apple silicon (M1 and later) | `octopus-mcp-<version>-darwin-arm64.mcpb` |
   | Mac with an Intel processor | `octopus-mcp-<version>-darwin-amd64.mcpb` |
   | Windows on x64 (most PCs) | `octopus-mcp-<version>-windows-amd64.mcpb` |
   | Windows on Arm | `octopus-mcp-<version>-windows-arm64.mcpb` |

   There are Linux bundles too (`linux-amd64`, `linux-arm64`) for other clients that install MCP Bundles.

2. Double-click the file, or drag it onto **Settings → Extensions** in Claude Desktop, and click **Install**.
3. Fill in the settings and turn the extension on. The tools appear under the tools (🔨) menu, and the prompts under the **+** menu. You can allow or block each tool separately there.

To update, install the bundle from a newer release the same way.

### Settings

| Setting | Variable | What to enter |
|---|---|---|
| **API key** (required) | `OCTOPUS_API_KEY` | Your API key, from **[Personal details → API access](https://octopus.energy/dashboard/new/accounts/personal-details/api-access)**. It starts `sk_live_`. Claude Desktop keeps it in your system's secure storage. |
| **Account number** | `OCTOPUS_ACCOUNT` | Your account number, like `A-1234ABCD`. Only needed if your login has more than one account. |
| **Allow changes** | `OCTOPUS_ALLOW_WRITES` | Off by default, which keeps Claude read-only. On lets it change Intelligent Octopus charging and join Saving Sessions. Read [Security](security.md) before turning it on. |
| **Toolsets** | `OCTOPUS_TOOLSETS` | Optional, such as `account,consumption,tariffs`. Empty means all, which is about 23 KB of the model's context read-only and 27 KB with changes allowed. See [Keeping the tool list small](configuration.md#keeping-the-tool-list-small). |
| **Audit log file** | `OCTOPUS_AUDIT_LOG` | Optional full path to a file, such as `/Users/you/octopus-mcp-audit.jsonl`, that records every change Claude makes. The folder must exist; the file is created. `stderr` writes the entries to the extension's log instead. Off when empty. See [Audit log](configuration.md#audit-log). |

Settings you leave empty use their defaults, as described in [Configuration](configuration.md).

### If it doesn't start

- **Read the error.** Claude Desktop shows that the extension failed to start. The server's log, which says what's wrong (for example a missing API key or a bad account number), is in `~/Library/Logs/Claude/` on macOS and `%APPDATA%\Claude\logs\` on Windows, in a file named after the extension.
- **macOS blocks the server.** The binary in the bundle isn't notarised by Apple. If macOS says it can't check it for malicious software, open **System Settings → Privacy & Security** and click **Open Anyway** next to the message about `octopus-mcp`, then turn the extension off and on again.

### Or configure it by hand

If you'd rather run the container image or your own binary, use [stdio mode](#stdio-mode). Open **Settings → Developer → Edit Config** and add:

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

Clients that install servers from the [MCP Registry](https://registry.modelcontextprotocol.io/v0/servers?search=io.github.mattoddie/octopus-mcp) can find octopus-mcp there as `io.github.mattoddie/octopus-mcp`. The entry offers the Docker image in [stdio mode](#stdio-mode) and the extension bundles, and lists the same settings as the [configuration](configuration.md) variables.

Any client that supports MCP **Streamable HTTP** works. Point it at `http://<docker-host>:8080/mcp` and send the bearer token header.

If your client only supports stdio, you have two options. You can use [stdio mode](#stdio-mode), or you can keep the shared HTTP server and connect through a stdio-to-HTTP bridge such as [`mcp-remote`](https://www.npmjs.com/package/mcp-remote):

```json
{
  "command": "npx",
  "args": ["-y", "mcp-remote", "http://<docker-host>:8080/mcp", "--header", "Authorization: Bearer <token>"]
}
```

Some clients limit how many tools they accept, or slow down with a lot of tools. If yours does, narrow `OCTOPUS_TOOLSETS`. See [Configuration](configuration.md#keeping-the-tool-list-small).

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
