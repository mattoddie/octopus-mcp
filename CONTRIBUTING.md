# Contributing to octopus-mcp

Thanks for wanting to help! octopus-mcp is a community project, and every kind of contribution is welcome: bug reports, notes on how the Octopus APIs behave, documentation fixes, new tools and code review.

By taking part you agree to follow the [code of conduct](CODE_OF_CONDUCT.md).

- [Ways to contribute](#ways-to-contribute)
- [Development setup](#development-setup)
- [Making a change](#making-a-change)
- [Pull request guidelines](#pull-request-guidelines)
- [Commit messages](#commit-messages)
- [Adding or changing tools](#adding-or-changing-tools)
- [Releases](#releases)
- [Getting help](#getting-help)

## Ways to contribute

- **Report a bug.** Use the [bug report form](https://github.com/mattoddie/octopus-mcp/issues/new?template=bug_report.yml). Include the octopus-mcp version, the tool involved and the exact error, and strip out your API key, account number, MPAN/MPRN, meter serial numbers, device IDs, address and any other private details.
- **Share what you've learned about the Octopus APIs.** They have quirks, such as which units a meter reports in, or GraphQL fields that only work for some devices. A note in an issue or [discussion](https://github.com/mattoddie/octopus-mcp/discussions) saying *"this field is empty for SMETS1 meters"* is genuinely useful.
- **Suggest a feature.** Open a [feature request](https://github.com/mattoddie/octopus-mcp/issues/new?template=feature_request.yml). If you know the API endpoint or GraphQL field involved, from [Octopus's API documentation](https://docs.octopus.energy/rest/guides/endpoints) or the [GraphQL schema](https://api.octopus.energy/v1/graphql/), include it. It's often most of the work.
- **Improve the docs.** Typos, unclear steps and missing examples are all fair game. Small docs fixes can go straight to a pull request.
- **Write code.** Look for issues labelled [`good first issue`](https://github.com/mattoddie/octopus-mcp/labels/good%20first%20issue) or [`help wanted`](https://github.com/mattoddie/octopus-mcp/labels/help%20wanted). For anything big, please open an issue first so we can agree on the approach before you invest time.
- **Report a vulnerability privately.** See [SECURITY.md](SECURITY.md). Please don't open a public issue for it.

## Development setup

You need **Go 1.25+** and, to build the image, **Docker**.

```sh
git clone https://github.com/<you>/octopus-mcp.git   # your fork
cd octopus-mcp
make test
```

`make test` runs the whole suite against a built-in mock of the Octopus REST and GraphQL APIs in a few seconds. You don't need an Octopus account or API key to contribute.

See [docs/development.md](docs/development.md) for the architecture, the mock and a walkthrough of adding a tool.

## Making a change

1. **Fork** the repository and create a branch from `main`, for example `git checkout -b feat/heat-pump-status`.
2. **Make your change** with tests. Keep it focused: one feature or fix per pull request.
3. **Run the checks** that CI runs:

   ```sh
   make test         # go test ./...
   make lint         # gofmt and go vet
   make docs-check   # docs/tools.md matches the code
   make image        # optional: the image builds and the tests pass inside it
   ```

4. **Update the docs.** If you changed tools, run `make docs`. Update `README.md` or `docs/` if behaviour changed, and add a line under **Unreleased** in [CHANGELOG.md](CHANGELOG.md) for anything users will notice.
5. **Open a pull request** against `main` and fill in the template.

## Pull request guidelines

- CI must be green. It runs the tests, lint and the generated-docs check, builds the image for amd64 and arm64, and smoke-tests the container.
- Match the existing style: `gofmt`, small focused functions, errors that tell the user what to do, and comments that explain *why* rather than *what*.
- **No new dependencies without discussion.** The project deliberately stays small. The only direct dependencies are the MCP Go SDK and `jsonschema-go`.
- Never include a real API key, account number, MPAN/MPRN, meter serial number, device ID, address or real usage data in code, tests, fixtures or screenshots. Use obviously fake values like `A-1234ABCD`, `1000000000001` and `1 Example Street`.
- Maintainers may push small fixes to your branch or squash commits when merging.
- Label suggestions (`bug`, `enhancement`, `new-tool`, `documentation`, `breaking`, …) are welcome. Labels sort the auto-generated release notes.

## Commit messages

- Use the imperative mood with a subject of 72 characters or fewer, for example *"Add heat pump status tool"* or *"Fix gas cost when the meter reports kWh"*.
- Add a body that explains **why** when it isn't obvious.
- Reference issues with `Fixes #123` where relevant.

## Adding or changing tools

The full guide is in [docs/development.md](docs/development.md#adding-a-tool). The short version:

- Put the tool in the right toolset file under `internal/tools/`, using `add[In]()` with a `Def` and an input struct whose `json` and `jsonschema` tags define the arguments.
- Set its `Gate` correctly: `Write` for **anything** that changes state at Octopus or in the home, such as EV charging settings or joining a Saving Session. Set `OpenWorld` when it affects something physical, like a car starting to charge, and `Destructive` when it can't be undone. Octopus API keys can't be limited to read-only, so **this gating is the project's main safety feature, and reviewers check it carefully.**
- Use GraphQL variables, never values pasted into query text, and check every field against the live schema. Avoid deprecated fields.
- Put units in output field names (`_p_per_kwh`, `_gbp`, `_kwh`), use UK time for output, and say what was assumed.
- Return compact summaries, and never return the API key or tokens.
- Write the description for an AI model: what the tool does, side effects, where IDs come from, units, and Octopus's own limits.
- Add tests against the mock, then run `make docs`.

Changing an existing tool's name or arguments in an incompatible way, or moving it to a less restrictive gate, is a **breaking change**. Call it out in the PR and the changelog.

## Releases

Releases are made by maintainers by publishing a GitHub release. That triggers the workflow that builds and pushes the Docker image. See [docs/releasing.md](docs/releasing.md).

## Getting help

Ask in [GitHub Discussions](https://github.com/mattoddie/octopus-mcp/discussions), or comment on the issue you're working on. No question is too small.
