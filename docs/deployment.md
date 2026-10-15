# Deployment

- [Docker Compose](#docker-compose)
- [Commands](#commands)
- [Image tags](#image-tags)
- [Upgrading](#upgrading)
- [Building from source](#building-from-source)
- [TLS with a reverse proxy](#tls-with-a-reverse-proxy)
- [Hardening](#hardening)
- [Running without Docker](#running-without-docker)

## Docker Compose

The repository's [`compose.yaml`](../compose.yaml) is a good starting point:

```yaml
services:
  octopus-mcp:
    image: ghcr.io/mattoddie/octopus-mcp:latest
    container_name: octopus-mcp
    restart: unless-stopped
    env_file: .env
    ports:
      - "8080:8080"
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
```

```sh
docker compose up -d          # start
docker compose logs -f        # follow logs
docker compose ps             # shows "healthy" once the health check passes
docker compose down           # stop
```

The server keeps no state: the API key comes from the environment and Kraken tokens live in memory. There's no volume to look after, the root filesystem can be read-only, and you can run as many copies as you like.

The image has a built-in health check that runs `octopus-mcp healthcheck` every 30 seconds. The image has no shell or `curl`, so the binary checks its own `/health` endpoint. It only shows that the server is running, not that Octopus is reachable or the API key is valid.

To listen on one interface only, such as a LAN address or localhost when you use a reverse proxy, change the port mapping to `"192.168.1.20:8080:8080"` or `"127.0.0.1:8080:8080"`.

## Commands

The image's entrypoint is the `octopus-mcp` binary, so you can run any of its commands with `docker compose run --rm octopus-mcp <command>`, or `docker compose exec octopus-mcp /octopus-mcp <command>` while it's running:

| Command | What it does |
|---|---|
| `serve` (the default) | Serves MCP over stdio, or Streamable HTTP when `MCP_TRANSPORT=http` |
| `status` | Checks the API key by getting a Kraken token, and prints the accounts it can see as JSON. Never prints the key or token. |
| `healthcheck` | Exits with `0` if the HTTP server answers `/health`. Used by the image's `HEALTHCHECK`. |
| `version` | Prints the version |

## Image tags

Images are published to the GitHub Container Registry at **`ghcr.io/mattoddie/octopus-mcp`** for `linux/amd64` and `linux/arm64`. Each [GitHub release](https://github.com/mattoddie/octopus-mcp/releases) publishes these tags:

| Tag | Example | Moves? |
|---|---|---|
| `X.Y.Z` | `1.4.2` | Never. Pin this for full reproducibility. |
| `X.Y` | `1.4` | Moves to the latest patch release (bug fixes). |
| `X` | `1` | Moves to the latest minor release (new features, no breaking changes). Not published for `0.x`. |
| `latest` | | Moves to the newest stable release. |

Pre-releases, such as `1.5.0-rc.1`, are only published under their exact version, never as `latest`.

The image is built `FROM gcr.io/distroless/static-debian13:nonroot`. It contains the static `octopus-mcp` binary, CA certificates and time zone data, and nothing else: no shell and no package manager. It runs as the non-root user `65532` and is about 20 MB.

Every image includes an SBOM and build provenance attestation. To verify that an image was built by this repository's release workflow:

```sh
gh attestation verify oci://ghcr.io/mattoddie/octopus-mcp:1.4.2 --owner mattoddie
```

## Upgrading

```sh
docker compose pull
docker compose up -d
```

Check the [changelog](../CHANGELOG.md) or the release notes before upgrading across a major version. After an upgrade, reconnect or restart your MCP client so it fetches the new tool list.

## Building from source

```sh
git clone https://github.com/mattoddie/octopus-mcp.git
cd octopus-mcp
docker build -t octopus-mcp .        # or: make image
```

The tests run as part of the image build, so a broken build never produces an image. The build stage cross-compiles, so building for another architecture doesn't need emulation. To use a local build with Compose, replace `image:` in `compose.yaml` with `build: .` and run `docker compose up -d --build`.

## TLS with a reverse proxy

The server speaks plain HTTP. If it's reachable from outside a trusted network, put a TLS-terminating reverse proxy in front of it and keep `MCP_AUTH_TOKEN` set.

**Caddy**, which gets certificates automatically:

```text
octopus-mcp.example.com {
    reverse_proxy octopus-mcp:8080
}
```

**nginx:**

```nginx
server {
    listen 443 ssl;
    server_name octopus-mcp.example.com;
    ssl_certificate     /etc/ssl/certs/octopus-mcp.pem;
    ssl_certificate_key /etc/ssl/private/octopus-mcp.key;

    location /mcp {
        proxy_pass http://octopus-mcp:8080;
        proxy_set_header Host $host;
        proxy_read_timeout 120s;
    }
}
```

**Traefik** labels:

```yaml
services:
  octopus-mcp:
    image: ghcr.io/mattoddie/octopus-mcp:latest
    env_file: .env
    labels:
      - traefik.enable=true
      - traefik.http.routers.octopus-mcp.rule=Host(`octopus-mcp.example.com`)
      - traefik.http.routers.octopus-mcp.tls.certresolver=letsencrypt
      - traefik.http.services.octopus-mcp.loadbalancer.server.port=8080
```

When the server sits behind a proxy, don't publish its port. Set `MCP_ALLOWED_HOSTS=octopus-mcp.example.com` so it only accepts requests for that hostname.

Most tools make one or a few Octopus requests, each limited by `OCTOPUS_TIMEOUT_MS` (30 seconds by default). Consumption over long periods and cost estimates fetch several pages of readings and rates, so give the proxy a read timeout of a minute or two.

> [!WARNING]
> Don't expose octopus-mcp directly to the internet. Anyone with the `MCP_AUTH_TOKEN` can read your energy data and, if writes are on, control your EV charging. For remote access, a VPN (WireGuard, Tailscale) is safer than a public endpoint.

## Hardening

The image already runs as a non-root user with nothing in it but the binary, and the default `compose.yaml` already sets `read_only`, `cap_drop: [ALL]` and `no-new-privileges`. You can lock it down further:

```yaml
services:
  octopus-mcp:
    image: ghcr.io/mattoddie/octopus-mcp:1.4.2   # pinned
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    mem_limit: 128m
    pids_limit: 100
    env_file: .env
    ports: ["127.0.0.1:8080:8080"]
```

The server only needs outbound HTTPS to `api.octopus.energy` and `api.backend.octopus.energy`. See [Security](security.md) for choosing settings and toolsets.

## Running without Docker

Docker is the easiest way to run octopus-mcp as a service, but the server is a single static binary with no dependencies. You need [Go](https://go.dev/dl/) 1.25 or later to build it.

With `go install`:

```sh
go install github.com/mattoddie/octopus-mcp/cmd/octopus-mcp@latest
```

The binary lands in `$(go env GOPATH)/bin`. Built this way it reports its version as `0.0.0-dev`.

Or from a clone, which also stamps the version:

```sh
git clone https://github.com/mattoddie/octopus-mcp.git
cd octopus-mcp
make build VERSION=1.2.3      # writes bin/octopus-mcp
```

Then:

```sh
export OCTOPUS_API_KEY=sk_live_...
octopus-mcp status            # check the key
octopus-mcp                   # stdio, for an MCP client to start
MCP_TRANSPORT=http MCP_AUTH_TOKEN=... octopus-mcp   # or a long-running HTTP server
```

To run it as a service, a systemd unit that sets these variables in an `EnvironmentFile` (readable only by the service user) and runs `octopus-mcp` as an unprivileged user works well.
