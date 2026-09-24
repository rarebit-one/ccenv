# ccenv

`ccenv` launches Claude Code with the right existing login for the current directory. Each profile points at its own `CLAUDE_CONFIG_DIR`; credentials, settings, plugins, and session history stay in their original directories. It never copies or swaps credentials.

The selected profile comes from the nearest `.ccenv` in the current directory or any parent. If there is no dotfile, `ccenv` uses the global default. An invalid or unknown dotfile stops the launch instead of silently choosing another account. Before launching, `ccenv` asks Claude Code for its authentication status and checks the email and organization recorded when the profile was added. See [selection and verification behavior](docs/behavior.md) for the exact rules.

## Install and update

You need an installed `claude` command and a Linux or macOS machine. This is a
private repository, so both install methods require read access to
`rarebit-one/ccenv` and GitHub authentication. Linux is covered by CI; macOS
binaries are built for both Apple Silicon and Intel.

**With Go 1.23 or newer**, authenticate Git for the private module, then install
the latest tagged release into `~/.local/bin`:

```sh
gh auth login            # skip if already authenticated
gh auth setup-git        # lets Go's Git fetch use your gh login over HTTPS
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" GOPRIVATE=github.com/rarebit-one/ccenv \
  go install github.com/rarebit-one/ccenv@latest
ccenv version
```

Run the same `go install` command again to update. To pin a version, replace
`@latest` with a tag such as `@v0.1.0`. `GOPRIVATE` keeps this private module
away from the public Go module proxy and checksum database.

**Without Go**, download the installer from the latest release, review it, and
run it. The script selects the Linux or macOS binary for your CPU, verifies its
SHA-256 checksum, and installs it into `~/.local/bin`:

```sh
gh auth login            # skip if already authenticated
tag="$(gh release view -R rarebit-one/ccenv --json tagName --jq .tagName)"
gh release download "$tag" -R rarebit-one/ccenv -p install.sh \
  -O ./ccenv-install.sh --clobber
gh release verify-asset "$tag" ./ccenv-install.sh -R rarebit-one/ccenv
less ./ccenv-install.sh
bash ./ccenv-install.sh
ccenv version
```

Run `bash ./ccenv-install.sh` again to update. Use `--version v0.1.0` to pin a
release or `--bin-dir DIR` to choose another install directory. The installer
does not use sudo, edit shell files, read Claude profiles, or enable automatic
updates or telemetry. It downloads one release archive and its checksum through
the GitHub CLI, verifies the release attestation for immutable releases, then
replaces only the `ccenv` binary. The checksum detects a
damaged or altered archive, but it does not independently authenticate a
compromised GitHub release; the repository and authenticated GitHub connection
remain the trust boundary. Transient download failures are retried three times;
the installed binary is left alone if all attempts fail. Releases after v0.1.2
are immutable on GitHub, and the installer also verifies their GitHub release
attestation. Older releases get checksum verification only.

Ensure `~/.local/bin` is on your `PATH`. Add this to `~/.bashrc` to make `cc`
your interactive launch command:

```sh
eval "$(ccenv init bash)"
```

Open a new shell or source `~/.bashrc`, then run `ccenv init bash` to inspect the function if desired. `cc` is already the conventional C compiler command. The Bash function applies only to the shell that loads it, so builds and scripts can continue to use `/usr/bin/cc`. In that shell, `command cc` bypasses the function and runs the C compiler. `ccenv run --` works without shell setup.

## Configure existing profiles

Register the directories you already use, without moving them:

```sh
ccenv add work ~/.claude-work
ccenv add personal ~/.claude-personal
ccenv default personal
ccenv check
```

`add` records the account's current email and organization. Check its output before binding projects: if a directory is logged into the wrong account, sign in correctly using `CLAUDE_CONFIG_DIR=<directory> claude auth login`, then run `ccenv refresh <name>` to pin the corrected login. `ccenv check` reports profile identity changes and flags profiles that currently point to the same account. Duplicate accounts are reported but do not make `check` fail, since several profile names may intentionally use one login.

In a project, run `ccenv local work` to write a one-line `.ccenv` file:

```text
work
```

That selector applies to the project and its descendants. A closer `.ccenv` overrides it. Profile names are local to your machine, so decide whether to commit the selector to a shared repository. You can put one in a parent folder that holds several repositories.

```sh
cc                 # launch Claude Code using the selected profile
cc -c              # pass arguments through to Claude Code
ccenv current      # show the profile, config directory, and selection source
ccenv list         # list profiles; * marks the global default
ccenv run --profile work -- -p 'hello'  # one-off override
```

`--profile` chooses a config directory and its pinned login for one launch. To
launch despite a changed login, use `ccenv run --profile work --ignore-pin --`.
This bypasses the identity pin for that launch only and prints the account
Claude actually reports. It still requires a logged-in `claude.ai` account and
does not change the saved pin or switch credentials. `ccenv check` will
continue to flag the profile until its original login is restored or you
intentionally run `ccenv refresh`.

If no `.ccenv` and no global default exist, `cc` stops with an error. Run `ccenv default <name>` to set the fallback. The explicit `--profile` option works even when a local selector is invalid, so you can still launch a chosen profile while fixing the dotfile.

## Commands

| Command | Effect |
| --- | --- |
| `ccenv add NAME DIR` | Register an existing logged-in Claude config directory and pin its current account identity. |
| `ccenv refresh NAME` | Re-pin the account identity after you intentionally sign into that directory again. |
| `ccenv default NAME` | Set the profile used when no project selector exists. |
| `ccenv local NAME` | Write `.ccenv` in the current directory. |
| `ccenv current` | Show the selected name, config directory, and source of the selection. |
| `ccenv list` | List profiles; `*` marks the global default. |
| `ccenv check` | Verify every registered login and report profiles using the same account. |
| `ccenv run -- [CLAUDE_ARGS...]` | Launch Claude with the selected profile, forwarding arguments unchanged. |
| `ccenv run --profile NAME -- [CLAUDE_ARGS...]` | Launch once with an explicit profile. |
| `ccenv run --ignore-pin -- [CLAUDE_ARGS...]` | For one launch, bypass the selected profile's account pin while showing the reported login. |
| `ccenv init bash` | Print the interactive `cc` shell function. |
| `ccenv version` | Print the installed version. |

`ccenv` refuses to launch when common API-key, OAuth-token, gateway, or cloud-provider environment variables are set, because they can bypass the subscription selected by the profile. Run `ccenv current` if the selected profile is surprising, and `ccenv check` if Claude reports the wrong login.

## Files and overrides

- `~/.config/ccenv/config.json`: global default, profile paths, and expected account identities. Respects `XDG_CONFIG_HOME`.
- `.ccenv`: one profile name, found by walking from the working directory to the filesystem root.
- `CCENV_CONFIG`: use a different global config path, useful for testing.
- `CCENV_CLAUDE_BIN`: explicit path to the Claude Code executable, if it is not discoverable on `PATH`.

The global config stores directory paths, account emails, and organization IDs, but no tokens. It is written with mode `0600`. `ccenv` checks account identity at launch, but it cannot tell whether an account was already wrong when you registered it. Claude Code also applies project settings independently of the selected user config directory. The `cc` shell function covers terminal launches; other programs that start Claude directly need their own `ccenv run` integration.

## Development

```sh
go test -race ./...
go vet ./...
go build ./...
```

The end-to-end test builds `ccenv` and uses a fake Claude executable. It does not contact Anthropic or read your real profile directories.
GitHub Actions checks formatting, runs those commands on pushes and pull requests,
and uses Rarebit's shared action-pinning gate on pull requests. Dependabot
checks Go modules and GitHub Actions weekly.

Pushing a `vX.Y.Z` tag on `main` runs the release workflow. It verifies the tag
is on `main`, runs the Go checks, and publishes Linux and macOS archives plus
SHA-256 checksums and `install.sh` to [GitHub Releases](https://github.com/rarebit-one/ccenv/releases).
Release configuration lives in `.goreleaser.yaml`. GitHub release immutability
protects future releases from asset replacement and tag movement after
publication; it does not apply retroactively to v0.1.0–v0.1.2.
