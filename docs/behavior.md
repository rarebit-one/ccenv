# Selection and verification behavior

`ccenv` chooses a profile each time it launches Claude Code. It does not rely on a `cd` hook or a persistent shell variable. This lets one terminal run different projects concurrently without changing a shared active account.

## Selection order

| Priority | Source | Example |
| --- | --- | --- |
| 1 | `ccenv run --profile NAME -- ...` | One launch uses `NAME`, regardless of dotfiles. |
| 2 | Nearest `.ccenv` at or above the working directory | A selector in `/work/team` applies to `/work/team/project/src`. |
| 3 | `default` in the user config | Used only when there is no selector on the path. |

The directory search includes the current directory and walks to the filesystem root. A nearer file wins. A selector may contain blank lines and `#` comments, but exactly one non-comment profile name. Names start with an ASCII letter or digit and may then contain ASCII letters, digits, `.`, `_`, and `-`. The file cannot specify a directory path or shell command.

An empty, invalid, or unknown selector is an error. `ccenv` does not fall back to a parent selector or global default in that case. With no selector and no default, it also stops. This prevents an unnoticed typo from launching another account.

### Folder proxy settings

A `.ccenv` can also contain one JSON object:

```json
{
  "profile": "rarebit",
  "proxy": {
    "url": "http://127.0.0.1:18321",
    "api_key_file": "~/.config/ccenv/keys/rarebit",
    "model": "claude-sonnet-4-6",
    "timeout_seconds": 3
  }
}
```

The nearest selector wins as a whole. A child one-line selector chooses direct
login and does not inherit its parent's proxy. An explicit `--profile` override
also chooses the registered direct profile without inheriting folder settings.
Unknown JSON fields, invalid profiles, malformed settings, and multiple JSON
objects stop selection. Key paths may be absolute, start with `~/`, or be
relative to the selector's directory. The key file must be a regular file with
no group or other permissions, containing one nonempty key of at most 4096 bytes.
Inline keys are not accepted.

Proxy URLs require HTTPS or loopback HTTP and cannot contain embedded
credentials, a query, a fragment, or a trailing `/v1`. The preflight sends the
client key to `GET <url>/v1/models`, refuses redirects, and requires a nonempty
model catalog. An optional `model` must be advertised by that catalog. The
preflight timeout defaults to three seconds and accepts values from one to 30.

On success, ccenv launches Claude with the selected config directory,
`ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, and the optional `ANTHROPIC_MODEL`.
It does not check the local login in this branch. The client key authenticates
to the configured endpoint; the gateway owns upstream account selection. A
successful catalog request does not prove a particular upstream identity,
available quota, or successful inference.

On a connection, HTTP, catalog, or model failure, ccenv opens `/dev/tty` and
offers a direct launch with the selected profile's email and organization.
Only a complete `y` or `yes` response accepts. Blank input, no, EOF, and a
missing terminal stop the launch. Piped Claude input is never used for consent.
The accepted direct launch follows the usual pin check and carries no proxy
settings. Invalid configuration and unreadable key files fail without prompting.
`--direct` skips proxy preflight and uses the same verified direct launch.
`--account` and `--ignore-pin` require `--direct` in a proxy-configured folder.
Caller credential and gateway environment overrides remain prohibited in both
modes. Proxy settings do not apply to Claude Desktop.

This preflight happens at process launch. ccenv replaces itself with Claude and
cannot switch a running session if the proxy fails later.

## Profile registration

`ccenv add NAME DIR` requires an existing directory and a logged-in `claude.ai` account. It runs `claude auth status --json` with `CLAUDE_CONFIG_DIR=DIR` and records the reported email and organization ID. The original directory stays in place. `ccenv refresh NAME` records a new identity after an intentional login change. Claude Code documents [`CLAUDE_CONFIG_DIR`](https://code.claude.com/docs/en/env-vars) for running multiple accounts side by side.

The generated user config is JSON at `~/.config/ccenv/config.json` on Linux, or under the directory returned by the OS user-config lookup. `XDG_CONFIG_HOME` affects that default path. `CCENV_CONFIG` can point to another file. For example:

```json
{
  "default": "personal",
  "profiles": {
    "personal": {
      "dir": "/home/alice/.claude-personal",
      "email": "alice@example.com",
      "org_id": "example-org-id"
    }
  }
}
```

This file contains account identifiers and paths, but no login tokens. New config files are created with mode `0600`. Credentials remain managed by Claude Code in each profile directory.

A profile may point at Claude Code's default directory, `~/.claude`. Claude
keeps that directory's account cache at `~/.claude.json`, outside it, and reads
`<dir>/.claude.json` instead whenever `CLAUDE_CONFIG_DIR` names a directory,
even `~/.claude`. In that layout the login still works but the account email and
organization read as empty. `ccenv` therefore leaves `CLAUDE_CONFIG_DIR` and
`CLAUDE_SECURESTORAGE_CONFIG_DIR` unset for the default directory, for both the
identity check and the launch. A split launch whose config is the default
directory seeds its overlay from `~/.claude.json`.

## What happens on launch

1. Resolve the profile using the order above.
2. Stop if a known API-key, OAuth-token, gateway, or cloud-provider variable is set in the caller's environment.
3. Run `claude auth status --json` for that profile, with a ten-second timeout.
4. Require a logged-in `claude.ai` account. Unless `--ignore-pin` was passed, require its email and organization to match the selected account profile's pin.
5. If config and account profiles differ, prepare a private overlay in the ccenv cache. Copy `.claude.json` into it, omit credential files, and symlink other config entries to the selected config profile. This keeps settings, plugins, memories, and history shared while writes to Claude's mutable account cache remain isolated. Set `CLAUDE_CONFIG_DIR` to that overlay and `CLAUDE_SECURESTORAGE_CONFIG_DIR` to the selected account profile, then execute Claude Code with the supplied arguments. If the profiles match, use the config profile directly.

By default, the config and account profiles are the same. `--profile NAME`
chooses a config profile; `--account NAME` chooses which registered profile's
credentials and identity pin to use for one launch. The `--account` mode needs
Claude Code 2.1.215 or later. It uses the undocumented
`CLAUDE_SECURESTORAGE_CONFIG_DIR` feature, described in this
[upstream documentation request](https://github.com/anthropics/claude-code/issues/79223).
Because Claude Code stores account display metadata in the config directory,
`/status` may show cached identity details from that directory when the
credential profile differs. `ccenv` prints the selected config/account pairing
before launch; the display limitation is tracked in this
[upstream report](https://github.com/anthropics/claude-code/issues/79222).

When `claude` on `PATH` is a symlink to `mise`, `ccenv` resolves the installed
Claude Code binary using `mise which claude` before the auth check and launch.
Running the shim itself would reapply the project's `mise.toml` environment and
could overwrite the selected `CLAUDE_CONFIG_DIR`. `CCENV_CLAUDE_BIN` remains an
explicit executable override.

The environment check covers `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, `CLAUDE_CODE_OAUTH_TOKEN`, `CLAUDE_CODE_OAUTH_REFRESH_TOKEN`, and the listed `CLAUDE_CODE_USE_*` cloud-provider switches in the source. Other Claude settings, including project settings and managed settings, are resolved by Claude Code itself.

`ccenv check` checks every registered profile and reports identity mismatches. Among profiles that pass verification, it also reports names that share the same email and organization. Duplicate identity is a warning rather than a failure because it can be intentional.

`--ignore-pin` bypasses the identity comparison for one launch only. It prints
the account Claude reports before launching, even when that account matches the
saved pin. It does not change the login, the saved pin, or the selected config
directory. Without the flag, the selected profile's pin remains mandatory.

## Claude Desktop

`ccenv desktop` uses the same selection order as `ccenv run`, and the same
environment check. It launches `claude-desktop` (or `CCENV_DESKTOP_BIN`) with
`--user-data-dir` set to the profile's `desktop_dir`, or to
`$XDG_DATA_HOME/ccenv/desktop/NAME` when none is set. It also sets
`CLAUDE_CONFIG_DIR` and `CLAUDE_SECURESTORAGE_CONFIG_DIR` to the profile
directory. It drops the variables a running Claude Code session exports
(`CLAUDECODE`, `CLAUDE_PID`, `CLAUDE_CODE_SESSION_ID`,
`CLAUDE_CODE_MESSAGING_*`, and similar), so a launch from a Claude Code terminal
does not leak that session into Desktop. Desktop passes its own login to Code-tab
sessions as `CLAUDE_CODE_OAUTH_TOKEN`; the profile directory supplies their
settings, memory, plugins, and history. Arguments after `--` reach Desktop
unchanged, except that a caller-supplied `--user-data-dir` is refused. The packaged app deletes
`CLAUDE_USER_DATA_DIR` from its environment at startup, so the command-line
switch is the supported lever.

Electron takes its single-instance lock inside the user data directory. A
second launch of a running profile therefore forwards its arguments, including
a `claude://` link, to that profile's window and exits. Launches of different
profiles run side by side. `ccenv` reads the lock symlink
(`SingletonLock -> HOST-PID`) to report whether a profile is running; a lock
from another host or a dead process does not count.

Desktop signs in on its own and offers no command that reports the signed-in
account, so a Desktop launch does not check the profile's pin.

On Linux, `ccenv desktop` replaces itself with Desktop, so a launcher's process
becomes the app. On macOS it finds `Claude.app/Contents/MacOS/Claude` in
`/Applications` or `~/Applications` unless `CCENV_DESKTOP_BIN` is set, and
starts Desktop in a new session (`setsid`) before exiting. Desktop quits on
`SIGHUP`, so this keeps it alive when the launching terminal closes. `open -a`
would also detach, but LaunchServices does not pass the environment that
carries `CLAUDE_CONFIG_DIR`.

`ccenv desktop handle [ARGS...]` is the launcher's entry point. Without a
`claude://` argument it launches the selected profile. With one, it picks a
profile in this order:

1. the only profile whose Desktop is running;
2. among several running profiles, the one ccenv launched most recently, then
   the global default, then the first by name;
3. with none running, the most recently launched profile, then the global
   default.

`ccenv desktop install` refuses to replace a `com.anthropic.Claude.desktop`
in `$XDG_DATA_HOME/applications` that it did not write. Entries it writes carry
`X-Ccenv-Managed=true`, which `install` and `uninstall` require before they
remove a file.

## Limits

- A profile's pinned identity proves what Claude Code reported when you registered or refreshed it. It cannot establish that you chose the right account at that moment. Inspect the output from `add`, `refresh`, and `check`.
- The `cc` function is for interactive Bash shells. Scripts can call `ccenv run -- ...` directly. GUI integrations that invoke `claude` themselves do not automatically use `ccenv`.
- Desktop logins are not pinned. A Desktop profile signed in to the wrong account is caught only by looking at Desktop's settings.
- `claude://` routing is a guess when more than one profile is running. Start sign-ins one profile at a time.
- On macOS, `claude://` links reach the app bundle directly, so `ccenv` cannot route them at all. Sign in with only the target profile's Desktop running.
- `ccenv` selects accounts at process launch. It does not switch a running Claude session or rotate accounts on usage limits.
- Profile directories isolate user-level data as supported by `CLAUDE_CONFIG_DIR`; project and managed settings may still apply to the same working directory.

### Account model defaults on a shared gateway

The optional `proxy.default_models` object maps `sonnet`, `opus`, and `haiku`
to advertised model IDs, such as `sidekick/claude-sonnet-4-6`. ccenv sets the
corresponding `ANTHROPIC_DEFAULT_*_MODEL` variables for proxy launches. When
`sonnet` is set, `CLAUDE_CODE_SUBAGENT_MODEL` uses that value too. All configured
model IDs must appear in the preflight catalog. These defaults override inherited
values only for the proxy process; direct fallback does not receive them.

Account aliases route requests within one gateway. They prevent ordinary model
and subagent selection from switching accounts, but are not a security boundary:
a caller with the gateway key can deliberately request another account's alias.
The gateway must keep restricted credentials out of its unprefixed model pool.

### Preserve a claude.ai login through a gateway

Set `proxy.auth_mode` to `"claudeai"` when the gateway accepts a separate
`x-api-key` header and routes to the intended Claude account. ccenv verifies the
local claude.ai email and organization pin before launch, sets
`ANTHROPIC_BASE_URL`, and sends the gateway key through
`ANTHROPIC_CUSTOM_HEADERS`. It does not set `ANTHROPIC_AUTH_TOKEN` or
`ANTHROPIC_API_KEY`, allowing Claude Code to retain its native subscription
identity and organization features. An inherited `ANTHROPIC_CUSTOM_HEADERS`
value is rejected in this mode to prevent conflicting authentication headers.

The default `"gateway"` mode continues to use `ANTHROPIC_AUTH_TOKEN` and needs
no local login while the proxy is available. A native login reported by Claude
Code does not verify a gateway's billing policy or serving account; the gateway
operator must independently verify upstream credentials and account routing.

See [Anthropic's gateway subscription behavior](https://code.claude.com/docs/en/gateways#subscriptions-and-gateways).
