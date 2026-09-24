# ccenv

`ccenv` launches Claude Code with the right existing login for the current directory. Each profile points at its own `CLAUDE_CONFIG_DIR`; credentials, settings, plugins, and session history stay in their original directories. It never copies or swaps credentials.

The selected profile comes from the nearest `.ccenv` in the current directory or any parent. If there is no dotfile, `ccenv` uses the global default. An invalid or unknown dotfile stops the launch instead of silently choosing another account. Before launching, `ccenv` asks Claude Code for its authentication status and checks the email and organization recorded when the profile was added.

## Install

Requires Go 1.23 or newer and an installed `claude` command.

```sh
go build -o ccenv .
install -m 755 ccenv ~/.local/bin/ccenv
```

Add this to `~/.bashrc` to make `cc` your interactive launch command:

```sh
eval "$(ccenv init bash)"
```

`cc` is already the conventional C compiler command. The Bash function applies only to your interactive shell, so builds and scripts can continue to use `/usr/bin/cc`. `ccenv run --` also works without shell setup.

## Configure existing profiles

Register the directories you already use, without moving them:

```sh
ccenv add work ~/.claude-work
ccenv add personal ~/.claude-personal
ccenv default personal
ccenv check
```

`add` records the account's current email and organization. Check its output before binding projects: if a directory is logged into the wrong account, sign in correctly using `CLAUDE_CONFIG_DIR=<directory> claude auth login`, then run `ccenv refresh <name>` to pin the corrected login. `ccenv check` reports profile identity changes and flags profiles that currently point to the same account.

In a project, run `ccenv local work` to write a one-line `.ccenv` file:

```text
work
```

That selector applies to the project and its descendants. A closer `.ccenv` overrides it. Profile names are local to your machine, so decide whether to commit the selector to a shared repository.

```sh
cc                 # launch Claude Code using the selected profile
cc -c              # pass arguments through to Claude Code
ccenv current      # show the profile, config directory, and selection source
ccenv list         # list profiles; * marks the global default
ccenv run --profile work -- -p 'hello'  # one-off override
```

`ccenv` refuses to launch when common API-key, OAuth-token, gateway, or cloud-provider environment variables are set, because they can bypass the subscription selected by the profile.

## Files and overrides

- `~/.config/ccenv/config.json`: global default, profile paths, and expected account identities. Respects `XDG_CONFIG_HOME`.
- `.ccenv`: one profile name, found by walking from the working directory to the filesystem root.
- `CCENV_CONFIG`: use a different global config path, useful for testing.
- `CCENV_CLAUDE_BIN`: explicit path to the Claude Code executable, if it is not discoverable on `PATH`.

`ccenv` checks account identity at launch, but it cannot tell whether an account was already wrong when you registered it. Claude Code also applies project settings independently of the selected user config directory.
