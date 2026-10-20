<!-- Title: use Conventional Commits, e.g. "fix(consumption): convert gas cost when the meter reports kWh". It becomes the commit on main. See CONTRIBUTING.md. -->

## What does this change?

<!-- A short description of the change and why it's needed. Link any related issue: "Fixes #123". -->

## How was it tested?

<!-- e.g. new tests against the mock Octopus API, `make test`, and/or tried against a real Octopus account. -->

## Checklist

- [ ] `make test` and `make lint` pass
- [ ] New or changed tools have tests, and `make docs` has been run
- [ ] New tools that change anything are gated with `Write`, with the right `Destructive` and `OpenWorld` flags
- [ ] Docs updated where behaviour changed (README / `docs/`)
- [ ] `CHANGELOG.md` has an entry under **Unreleased** for user-facing changes
- [ ] No API keys, account numbers, MPANs/MPRNs, serial numbers or real usage data in code, tests or screenshots
