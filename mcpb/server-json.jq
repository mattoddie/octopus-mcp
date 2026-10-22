# Stamps server.json (the MCP Registry entry) for a release, called by
# `make server-json`:
#
#   jq --arg version 1.2.3 --rawfile sums dist/SHA256SUMS -f mcpb/server-json.jq server.json
#
# $sums is `sha256sum *.mcpb` output for the release's bundles. The committed
# server.json carries 0.0.0-dev and zero hashes; this sets the version, the
# image tag, and each bundle's download URL and SHA-256. Every bundle listed
# must have a hash, and every hash must belong to a listed bundle.

($sums
  | split("\n")
  | map(select(length > 0)
        | capture("^(?<sha>[0-9a-f]{64}) [ *](.*/)?(?<file>[^/]+)$")
          // error("can't read the checksum line: \(.)")
        | {(.file): .sha})
  | add // {}) as $sha

| .version = $version
| .packages |= map(
    if .registryType == "oci" then
      .identifier |= sub(":[^:/]+$"; ":" + $version)
    elif .registryType == "mcpb" then
      (.identifier
        | capture("^(?<base>https://github\\.com/[^/]+/[^/]+/releases/download/)[^/]+/octopus-mcp-.+-(?<platform>[a-z]+-[a-z0-9]+)\\.mcpb$")
          // error("unexpected bundle URL: \(.)")) as $m
      | ("octopus-mcp-\($version)-\($m.platform).mcpb") as $file
      | .identifier = "\($m.base)v\($version)/\($file)"
      | .fileSha256 = ($sha[$file] // error("no SHA-256 for \($file)"))
    else . end)
| ([.packages[] | select(.registryType == "mcpb") | .identifier | split("/") | last]) as $listed
| ($sha | keys - $listed) as $unlisted
| if $unlisted != [] then error("bundles missing from server.json: \($unlisted | join(", "))") else . end
