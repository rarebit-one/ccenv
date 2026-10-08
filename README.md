# ccenv

[![Tests](https://github.com/rarebit-one/ccenv/actions/workflows/test.yml/badge.svg)](https://github.com/rarebit-one/ccenv/actions/workflows/test.yml)
[![Latest release](https://img.shields.io/github/v/release/rarebit-one/ccenv)](https://github.com/rarebit-one/ccenv/releases/latest)

`ccenv` launches Claude Code with the right existing login for the current directory. Each profile points at its own `CLAUDE_CONFIG_DIR`; credentials, settings, plugins, and session history stay in their original directories. It never copies or swaps credentials.

The selected profile comes from the nearest `.ccenv` in the current directory or any parent. If there is no dotfile, `ccenv` uses the global default. An invalid or unknown dotfile stops the launch instead of silently choosing another account. Direct launches check the email and organization recorded when the profile was added. A folder can instead select an authenticated proxy, with an interactive offer to use the pinned direct login when the proxy is unavailable. See [selection and verification behavior](docs/behavior.md) for the exact rules.

## Use a proxy for a folder

Keep the gateway client key in a private file outside the repository, with mode
`0600`. In the folder, run:

```sh
ccenv local rarebit --proxy-url http://127.0.0.1:18321 \
  --api-key-file '~/.config/ccenv/keys/rarebit' --model claude-sonnet-4-6
```

The command writes a JSON `.ccenv` containing the profile, URL, key-file path,
and optional model. The URL must use HTTPS or loopback HTTP and must omit `/v1`.
Use an inference endpoint, not a management-only UI proxy.

`cc` checks the authenticated model catalog before launching. When the proxy
cannot be used, it asks in the controlling terminal whether to launch with the
same profile's direct login. Only `y` or `yes` accepts. The direct launch checks
the local account pin and omits the proxy key, URL, and model. Without a terminal,
the launch stops. `cc --direct` explicitly skips the proxy. After updating ccenv,
open a new terminal or rerun `eval "$(ccenv init bash)"` to refresh the shortcut.

Successful proxy launches do not require a local Claude login. The gateway
controls upstream accounts; ccenv cannot pin a remote identity through the model
catalog. Existing one-line `.ccenv` selectors keep their direct-login behavior.
Run `ccenv local rarebit` to return the folder to that behavior.

## Install and update

You need an installed `claude` command and a Linux or macOS machine. Linux is
covered by CI; macOS binaries are built for both Apple Silicon and Intel.

**With Go 1.23 or newer**, install the latest tagged release into
`~/.local/bin`:

```sh
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" \
  go install github.com/rarebit-one/ccenv@latest
ccenv version
```

Run the same `go install` command again to update. To pin a version, replace
`@latest` with a tag such as `@v0.1.0`.

**Without Go**, install the GitHub CLI, authenticate if it asks, then download
the installer from the latest release, review it, and run it. The script
selects the Linux or macOS binary for your CPU, verifies its SHA-256 checksum,
and installs it into `~/.local/bin`:

```sh
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

## Quick start

Register the directories for existing Claude Code logins, choose the default,
and launch from any directory:

```sh
ccenv add personal ~/.claude-personal
ccenv add work ~/.claude-work
ccenv default personal
ccenv run --
```

To select a profile for one project, run `ccenv local work` from that project,
then launch with `ccenv run --`. The `.ccenv` file contains only the profile
name; commit it only when collaborators use the same local profile name.
`ccenv run --` works without shell setup. For the interactive `cc` command,
see the Bash setup below.

Ensure `~/.local/bin` is on your `PATH`. Add this to `~/.bashrc` to make `cc`
your interactive launch command:

```sh
eval "$(ccenv init bash)"
```

Open a new shell or source `~/.bashrc`, then run `ccenv init bash` to inspect the function if desired. `cc` is already the conventional C compiler command. The Bash function applies only to the shell that loads it, so builds and scripts can continue to use `/usr/bin/cc`. In that shell, `command cc` bypasses the function and runs the C compiler. `ccenv run --` works without shell setup.

If `claude` resolves to a `mise` shim, `ccenv` asks `mise which claude` for the
installed executable and launches it directly. This keeps a project's `mise.toml`
from replacing the selected `CLAUDE_CONFIG_DIR`. The same resolution applies
when `CCENV_CLAUDE_BIN` explicitly points to a `mise` shim; for other wrappers,
point it at the real Claude Code executable.

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
cc --account rarebit                   # use Rarebit credentials with this folder's config
cc --profile sidekick --ignore-pin      # bypass the pin for this launch only
```

`--profile` selects the user config directory. `--account` selects the
registered profile whose login credentials to use, while retaining the
selected project or user config directory. The `cc` Bash function accepts
`--profile`, `--account`, and `--ignore-pin` before Claude arguments; use `--`
to end ccenv options when needed. These selections apply only to that launch.
From a Sidekick project, for
example, `ccenv run --profile sidekick --account rarebit --` (or `cc --account
rarebit` after loading the Bash function) uses Sidekick settings, memories,
plugins, and history with credentials from the Rarebit profile. `--account`
checks Rarebit's saved identity pin before launch. This requires Claude Code
2.1.215 or newer and uses its currently undocumented
`CLAUDE_SECURESTORAGE_CONFIG_DIR` feature. See the [upstream documentation
request](https://github.com/anthropics/claude-code/issues/79223).

For a split launch, `ccenv` creates a private config overlay under
`$XDG_CACHE_HOME/ccenv/config-overlays` (or the OS cache directory). It copies
the config's mutable `.claude.json` account cache there and symlinks the other
config entries, so settings, plugins, memories, and history stay shared while
Claude's account-cache writes stay out of the configured profile. The overlay
is keyed by the config/account directory pair and has mode `0700`.

When the config and credential profiles differ, Claude Code's `/status` may
show the account cached in the config directory, rather than the account tied
to the credential store. `ccenv` prints the config/account pairing before
launch. Treat the `/status` email as cached metadata in this mode; see the
[upstream status report](https://github.com/anthropics/claude-code/issues/79222).

`--profile` chooses a config directory and its pinned login for one launch. To
launch despite a changed login, use `ccenv run --profile work --ignore-pin --`.
This bypasses the identity pin for that launch only and prints the account
Claude actually reports. It still requires a logged-in `claude.ai` account and
does not change the saved pin or switch credentials. `ccenv check` will
continue to flag the profile until its original login is restored or you
intentionally run `ccenv refresh`.

If no `.ccenv` and no global default exist, `cc` stops with an error. Run `ccenv default <name>` to set the fallback. The explicit `--profile` option works even when a local selector is invalid, so you can still launch a chosen profile while fixing the dotfile.

## Claude Desktop profiles

`ccenv desktop` starts the Claude desktop app with a profile of its own. Each
profile gets a separate Electron user data directory, so its Desktop login,
chats, settings, and connectors stay apart, and several profiles can run side
by side. Desktop's Code tab also receives the profile's `CLAUDE_CONFIG_DIR`, so
it shares settings, plugins, and history with that profile's terminal sessions.

```sh
ccenv desktop                      # selected profile (nearest .ccenv, then default)
ccenv desktop --profile sidekick   # explicit profile
ccenv desktop install              # app-menu launchers + claude:// routing (Linux)
ccenv discover                     # print `ccenv add` lines for unregistered ~/.claude* dirs
```

Desktop keeps its own login, separate from Claude Code's credentials, and has
no command that reports which account is signed in. `ccenv` therefore cannot
check a Desktop login against the profile's pin: sign in once per profile, and
check the account in Desktop's settings. Desktop's Code tab bills the account
signed in to Desktop, so keep each profile's Desktop login on the account its
pin names. The pin still guards every `ccenv run` launch.

`ccenv desktop install` writes a `Claude (NAME)` launcher for every profile to
`$XDG_DATA_HOME/applications` and removes launchers for profiles you have
unregistered. It also writes a hidden `com.anthropic.Claude.desktop` that
shadows the packaged entry and makes `ccenv desktop handle` the `claude://`
handler. Desktop's sign-in returns through a `claude://` link, which would
otherwise reach whichever profile the system launches by default. `ccenv`
sends each link to the only running profile, or else to the profile it
launched most recently, and that running window receives it. If you sign in to
two profiles at once, finish one before starting the other. `ccenv desktop
uninstall` removes only the entries ccenv wrote. Install the launchers again
after you add or remove a profile.

On Omarchy, `ccenv omarchy install` adds the `rarebit.ccenv` bar plugin, then
`omarchy plugin enable rarebit.ccenv` turns it on. It lists your profiles from
`ccenv list --json`, shows which ones have Desktop running, and opens a profile,
a new chat, or a new Claude Code session without a terminal. Reinstall it after
you move or upgrade the `ccenv` binary, because it records the binary's
absolute path, then run `omarchy-restart-shell`: the shell keeps the old path
cached across hot reloads.

Desktop on Linux is the official beta package (Debian and Ubuntu; Arch users
can install the AUR `claude-desktop` repackage). Set `CCENV_DESKTOP_BIN` if its
launcher is not `claude-desktop` on `PATH`.

On macOS, `ccenv desktop` finds `Claude.app` in `/Applications` or
`~/Applications` (or uses `CCENV_DESKTOP_BIN`), and starts it detached in its
own session, so closing the terminal does not quit it. Two limits apply there.
macOS delivers `claude://` links to the app bundle rather than to a command,
so ccenv cannot route a sign-in link to a profile: sign in with only that
profile's Desktop running. The launcher and Omarchy commands are Linux-only.

## Commands

| Command | Effect |
| --- | --- |
| `ccenv add NAME DIR` | Register an existing logged-in Claude config directory and pin its current account identity. |
| `ccenv refresh NAME` | Re-pin the account identity after you intentionally sign into that directory again. |
| `ccenv default NAME` | Set the profile used when no project selector exists. |
| `ccenv local NAME` | Write `.ccenv` in the current directory. |
| `ccenv current` | Show the selected name, config directory, and source of the selection. |
| `ccenv list [--json]` | List profiles; `*` marks the global default. `--json` adds each profile's Desktop data directory and whether Desktop is running, for launchers. |
| `ccenv discover` | Print an `ccenv add` command for each unregistered `~/.claude*` config directory. It reads file names only. |
| `ccenv check` | Verify every registered login and report profiles using the same account. |
| `ccenv run -- [CLAUDE_ARGS...]` | Launch Claude with the selected profile, forwarding arguments unchanged. |
| `ccenv run --profile NAME -- [CLAUDE_ARGS...]` | Launch once with an explicit profile. |
| `ccenv run --account NAME -- [CLAUDE_ARGS...]` | Use the selected config with another registered profile's pinned credentials. |
| `ccenv run --ignore-pin -- [CLAUDE_ARGS...]` | For one launch, bypass the selected profile's account pin while showing the reported login. |
| `ccenv desktop [--profile NAME] [-- ARGS...]` | Launch Claude Desktop with the profile's own user data directory and `CLAUDE_CONFIG_DIR`. |
| `ccenv desktop install` / `uninstall` | Write or remove per-profile app launchers and the `claude://` link router (Linux). |
| `ccenv omarchy install [--dir DIR]` | Install the Omarchy bar plugin `rarebit.ccenv`. |
| `ccenv init bash` | Print the interactive `cc` shell function. |
| `ccenv version` | Print the installed version. |

`ccenv` refuses to launch when common API-key, OAuth-token, gateway, or cloud-provider environment variables are set, because they can bypass the subscription selected by the profile. Run `ccenv current` if the selected profile is surprising, and `ccenv check` if Claude reports the wrong login.

## Files and overrides

- `~/.config/ccenv/config.json`: global default, profile paths, and expected account identities. Respects `XDG_CONFIG_HOME`.
- `.ccenv`: one profile name, found by walking from the working directory to the filesystem root.
- `CCENV_CONFIG`: use a different global config path, useful for testing.
- `CCENV_CLAUDE_BIN`: explicit path to the Claude Code executable, if it is not discoverable on `PATH`.
- `CCENV_DESKTOP_BIN`: explicit path to the Claude Desktop executable (default `claude-desktop` on `PATH`).
- `$XDG_DATA_HOME/ccenv/desktop/NAME`: a profile's Desktop user data directory, unless the profile sets `desktop_dir` in the config.
- `$XDG_STATE_HOME/ccenv/desktop-last`: the profile ccenv last launched in Desktop, used to route `claude://` links.

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

## License

Copyright (C) 2026 Jaryl Sim.

Licensed under the GNU General Public License, version 3 or (at your option)
any later version. See [LICENSE](LICENSE).
