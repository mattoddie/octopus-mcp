# Releasing

Releases are driven by **GitHub releases**. Publishing a release with a tag like `v1.2.3` triggers the [Release workflow](../.github/workflows/release.yml). That workflow builds the Docker image for `linux/amd64` and `linux/arm64` and pushes it to `ghcr.io/mattoddie/octopus-mcp`. It then builds the MCP Bundles (`.mcpb`) for Claude Desktop and attaches them to the release, and for stable releases publishes the server to the [MCP Registry](#the-mcp-registry). Nothing is published by merging to `main` alone.

- [Versioning](#versioning)
- [Release checklist](#release-checklist)
- [Pre-releases](#pre-releases)
- [What the workflow does](#what-the-workflow-does)
- [The MCP Registry](#the-mcp-registry)
- [If something goes wrong](#if-something-goes-wrong)
- [Repository setup](#repository-setup)

## Versioning

The project follows [Semantic Versioning](https://semver.org). Its public API is the set of **tool names, their arguments, prompts and the environment variables**:

| Change | Bump | Examples |
|---|---|---|
| Breaking | **major** (`2.0.0`) | Renaming or removing a tool, argument, prompt or variable; moving a tool to a less restrictive gate; changing a default so that more is allowed |
| New features | **minor** (`1.3.0`) | New tools, toolsets, arguments or prompts |
| Fixes | **patch** (`1.2.4`) | Bug fixes, compatibility fixes, docs, dependency updates |

While the version is `0.x`, minor bumps may contain breaking changes. Call them out in the release notes.

The version comes **from the tag**. There's no version file in the repository: builds default to `0.0.0-dev` (as does `mcpb/manifest.json`), and release builds stamp the tag's version into `internal/tools.Version` with `-ldflags`, and into each bundle's `manifest.json`. The server reports it to MCP clients, prints it on its first log line, and `octopus-mcp version` prints it.

## Release checklist

1. **Check `main` is green** on [CI](https://github.com/mattoddie/octopus-mcp/actions/workflows/ci.yml).
2. **Update the changelog.** In a small PR titled `chore(release): v1.2.0`, move the **Unreleased** entries in [`CHANGELOG.md`](../CHANGELOG.md) under a new heading, `## [1.2.0] - YYYY-MM-DD`, and update the comparison links at the bottom. Fill in the final tool and prompt counts from the top of [`docs/tools.md`](tools.md). Merge it.
3. **Publish the release.** Use either the CLI or the web UI.

   With the CLI:

   ```sh
   gh release create v1.2.0 --target main --title "v1.2.0" --generate-notes
   ```

   Or in the web UI: **Releases → Draft a new release → Choose a tag → type `v1.2.0` → Create new tag on publish**, target `main`. Click **Generate release notes**, edit them as needed, then **Publish release**.

4. **Watch the workflow.** Open **Actions → Release**. It takes a few minutes. When it finishes, the release notes gain a *Container image* section with the pull command, digest and tags, and a *Claude Desktop* section listing the bundles, which are attached as release assets. For a stable release, the *Publish to the MCP Registry* job runs last.
5. **Check the image:** `docker pull ghcr.io/mattoddie/octopus-mcp:1.2.0`, then `docker run --rm ghcr.io/mattoddie/octopus-mcp:1.2.0 version`. If you can, install the bundle for your computer in Claude Desktop too. For a stable release, check it appears in the [MCP Registry](https://registry.modelcontextprotocol.io/v0/servers?search=io.github.mattoddie/octopus-mcp).

Release notes are generated from merged PR titles and grouped by label. The groups are set in [`.github/release.yml`](../.github/release.yml). Label PRs `breaking`, `enhancement`, `new-tool`, `bug`, `compatibility`, `documentation` or `dependencies` to sort them, or `skip-changelog` to leave a PR out.

## Pre-releases

To test a release candidate, use a pre-release tag and tick **Set as a pre-release**:

```sh
gh release create v1.3.0-rc.1 --target main --prerelease --generate-notes
```

Pre-releases are published only under their exact version (`ghcr.io/mattoddie/octopus-mcp:1.3.0-rc.1`). They never move `latest`, `1` or `1.3`. They aren't published to the MCP Registry either.

## What the workflow does

| Step | Detail |
|---|---|
| Validate the tag | Must match `vMAJOR.MINOR.PATCH` with an optional `-prerelease` suffix, otherwise the workflow fails before building. |
| Build | `docker buildx` for `linux/amd64` and `linux/arm64`, from scratch with no build cache. The Go build cross-compiles, so no emulation is needed for the binary. The test suite runs inside the build, so failing tests mean no image. |
| Tag | `X.Y.Z` always. For stable releases also `X.Y`, `X` (from `1.0.0` on) and `latest`. |
| Push | To `ghcr.io/mattoddie/octopus-mcp` using the workflow's `GITHUB_TOKEN`. No personal tokens are needed. |
| Supply chain | Attaches an SBOM and SLSA provenance to the image, and pushes a signed build provenance attestation (`gh attestation verify`). |
| Release notes | Appends the pull command, digest and tags. |
| MCP Bundles | After the image, runs `make mcpb VERSION=X.Y.Z`: one `octopus-mcp-X.Y.Z-<os>-<arch>.mcpb` for each of `darwin`, `windows` and `linux` on `amd64` and `arm64`. It validates each manifest with the official `@anthropic-ai/mcpb` CLI (pinned in the workflow), attests their build provenance, uploads them to the release and lists them in the notes. |
| MCP Registry | Stable releases only, after the image and the bundles. Stamps [`server.json`](../server.json) with the version, the image tag and the SHA-256 of each bundle as uploaded, then publishes it with `mcp-publisher`. See [The MCP Registry](#the-mcp-registry). |

### About the bundles

An [MCP Bundle](https://github.com/modelcontextprotocol/mcpb) is a zip file with a `manifest.json`, the icon, the licence and the server binary. The manifest lives in [`mcpb/manifest.json`](../mcpb/manifest.json): edit its descriptions and settings by hand, while `make docs` regenerates its `tools` and `prompts` from the server (CI checks they're current). [`cmd/mcpb`](../cmd/mcpb/main.go) stamps the version and platform into a copy and writes the zip.

There's one bundle per OS and architecture because a manifest can only choose the command by OS, not by CPU. Try one locally with `make mcpb` (the bundles land in `dist/`). CI builds them on every pull request, validates the manifests and starts the Linux one over stdio.

The bundles aren't signed. The macOS binaries carry the Go linker's ad-hoc signature, which Apple silicon requires, but they aren't signed with a Developer ID or notarised, so macOS may ask the user to allow them (see [Claude Desktop](clients.md#if-it-doesnt-start)). `mcpb sign` can add a signature to the bundle itself, but that needs a code-signing certificate and isn't set up.

## The MCP Registry

Stable releases are listed in the official [MCP Registry](https://registry.modelcontextprotocol.io) as `io.github.mattoddie/octopus-mcp`, so clients and sub-registries that read it can find and install octopus-mcp. The entry is [`server.json`](../server.json) at the repository root. It offers two kinds of package:

- **The Docker image** (`ghcr.io/mattoddie/octopus-mcp:X.Y.Z`) over stdio. The image serves HTTP by default, so the entry sets `MCP_TRANSPORT=stdio` as a fixed value, and lists the settings a user fills in (`OCTOPUS_API_KEY`, required and secret, then `OCTOPUS_ACCOUNT`, `OCTOPUS_ALLOW_WRITES`, `OCTOPUS_TOOLSETS` and `OCTOPUS_AUDIT_LOG`), mirroring the bundle's settings. The registry checks that the image belongs to the server through its `io.modelcontextprotocol.server.name` label, set in the [`Dockerfile`](../Dockerfile).
- **The six bundles**, one entry each with its download URL and SHA-256. The registry's format has no field for the OS or architecture, so a client tells them apart by the `-<os>-<arch>.mcpb` file name. The image comes first, because it runs anywhere.

The committed `server.json` carries the placeholder version `0.0.0-dev` and zero hashes, like [`mcpb/manifest.json`](../mcpb/manifest.json), because the version comes from the tag. The release job stamps the real values into a copy (`make server-json`, using [`mcpb/server-json.jq`](../mcpb/server-json.jq)), from the bundles it downloads from the release. Edit descriptions and settings in the committed file. The Go tests fail if its settings drift from the manifest's, and CI checks it, and a stamped copy, with the registry's validator (`make registry-validate`).

The `registry` job in the [Release workflow](../.github/workflows/release.yml):

1. runs only for stable releases, after the image and bundle jobs, with `id-token: write` and `contents: read`;
2. downloads the release's bundles and runs `make server-json VERSION=X.Y.Z`;
3. installs `mcp-publisher`, pinned in the [`Makefile`](../Makefile) (`MCP_PUBLISHER_VERSION`) with the SHA-256 of each archive checked;
4. validates `dist/server.json`, logs in with GitHub OIDC (no secret needed) and publishes it, unless that version is already in the registry.

To update `mcp-publisher`, change `MCP_PUBLISHER_VERSION` and the `MCP_PUBLISHER_SHA256_*` values from the new [release's](https://github.com/modelcontextprotocol/registry/releases) assets (`gh release view vX.Y.Z -R modelcontextprotocol/registry --json assets --jq '.assets[] | .name + " " + .digest'`).

A version can be published only once, and its entry can't be changed afterwards. To correct an entry, release the next patch version.

## If something goes wrong

- **The workflow failed** for a transient reason: re-run it from the Actions tab. It's safe to re-run, and it pushes the same tags again.
- **The release was wrong** (bad code or wrong tag): don't move or reuse a published tag, because people may already have pulled it. Fix the problem on `main` and publish the next patch version. You can delete the bad GitHub release and mark it as such in the changelog, but leave the image tag alone.
- **Only the MCP Registry job failed** (for example the registry was down): open the run and choose **Re-run jobs → Re-run failed jobs**, which runs just that job against the published image and bundles. It skips the publish if the version is already listed. If its log shows a validation error, fix `server.json` on `main` and release the next patch version, because the job uses `server.json` as it was at the tag.
- **Publishing from the workflow keeps failing**: publish by hand from a checkout of the tag. A maintainer who is an owner of the `mattoddie` GitHub organisation can do this, because the registry grants the `io.github.mattoddie/*` namespace to its owners:

  ```sh
  git checkout v1.2.0
  rm -f dist/*.mcpb
  gh release download v1.2.0 --pattern '*.mcpb' --dir dist
  make server-json VERSION=1.2.0
  make registry-validate SERVER_JSON=dist/server.json
  make registry-publish SERVER_JSON=dist/server.json   # signs in through github.com in the browser
  ```

- **Wrong tag format** (e.g. `1.2.0` without the `v`): delete the release and tag (`gh release delete 1.2.0 --cleanup-tag`) and publish again with the right tag.

## Repository setup

### Rulesets

Two rulesets protect the repository. You can view and edit them under **Settings → Rules → Rulesets**, and that's where to create them if you're setting up a fresh copy.

**Protect main** (the default branch):

- Changes land only through pull requests. Nobody can push directly, force-push or delete the branch, and there are no bypasses.
- The CI checks must pass (tests, lint, the generated-docs check and the Docker image build), and the branch must be up to date with `main`.
- Review threads must be resolved before merging. The only merge method is squash.
- No approving review is required yet, because GitHub doesn't let authors approve their own pull requests and the project has a single maintainer. Raise this when there are more maintainers.

**Protect release tags** (`v*`):

- Only repository admins can create, move or delete version tags, so only maintainers can publish releases. Published versions can't be re-pointed.

> [!NOTE]
> Required checks are matched by job name. If you rename a job in [`ci.yml`](../.github/workflows/ci.yml), update the *Protect main* ruleset in the same change. Otherwise pull requests wait forever for a check that never reports.

### Container package visibility

New GHCR packages normally start private, and GitHub doesn't offer an API to change this. A package linked to a public repository usually becomes public automatically. If it starts private, open the package page (**Your profile → Packages → octopus-mcp → Package settings**) and choose **Change visibility → Public** under **Danger Zone**.

The package links to the repository automatically through the image's `org.opencontainers.image.source` label, which makes it show in the repository sidebar.
