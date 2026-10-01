# ccenv contributor guide

`ccenv` is a small Go CLI that selects a Claude Code profile from the nearest
`.ccenv` file or a global default. It must keep each profile's config directory
and login isolated.

## Development

Run `go test -race ./...`, `go vet ./...`, and `go build ./...` before pushing.
CI also checks `gofmt`. The integration test builds the CLI and uses a fake
Claude executable; tests must not read real profile directories or contact
Anthropic.

## Behavior to preserve

- An invalid or unknown `.ccenv` must stop selection rather than fall back to
  another account.
- Launch must verify the active Claude account against the identity recorded
  for the profile. Do not copy, swap, or store credentials in the global config.
- Arguments after `ccenv run --` must reach Claude unchanged.
- `ccenv desktop` must always set `--user-data-dir` from the profile and refuse
  one from the caller. Desktop logins cannot be pinned; say so rather than
  implying they are verified. `desktop install`/`uninstall` touch only entries
  marked `X-Ccenv-Managed=true`.
- `ccenv list --json` is the contract the embedded Omarchy plugin
  (`omarchy/rarebit.ccenv`) reads. Change both together.
- The interactive `cc` Bash function belongs in `ccenv init bash`; the bare
  `ccenv run --` command must work without shell setup.

See [docs/behavior.md](docs/behavior.md) for detailed selection and
verification rules. Update it and the tests when behavior changes.

## Releases

The module root is an installable Go command. A `vX.Y.Z` tag on `main` triggers
GoReleaser to publish Linux and macOS archives and checksums. Keep
`.goreleaser.yaml`, `.github/workflows/release.yml`, and the installation
instructions in `README.md` in sync. Test the release configuration with
`goreleaser check` and `goreleaser release --snapshot --clean --skip=publish`
before tagging. Run `bash scripts/tests/install.test.sh` after changing the
installer; CI exercises it on both Linux and macOS. Keep `scripts/install.sh`
as a release asset and include it in `checksums.txt`.
GitHub release immutability is enabled for this repository. GoReleaser uploads
assets while the release is a draft, then publishes it. Keep the GoReleaser
version pinned in both test and release workflows. The installer verifies the
release attestation for immutable releases.
