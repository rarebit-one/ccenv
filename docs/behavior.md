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

## What happens on launch

1. Resolve the profile using the order above.
2. Stop if a known API-key, OAuth-token, gateway, or cloud-provider variable is set in the caller's environment.
3. Run `claude auth status --json` for that profile, with a ten-second timeout.
4. Require a logged-in `claude.ai` account. Unless `--ignore-pin` was passed, require its email and organization to match the selected profile's pin.
5. Replace the caller's `CLAUDE_CONFIG_DIR` with the selected directory and execute Claude Code with the supplied arguments.

The environment check covers `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, `CLAUDE_CODE_OAUTH_TOKEN`, `CLAUDE_CODE_OAUTH_REFRESH_TOKEN`, and the listed `CLAUDE_CODE_USE_*` cloud-provider switches in the source. Other Claude settings, including project settings and managed settings, are resolved by Claude Code itself.

`ccenv check` checks every registered profile and reports identity mismatches. Among profiles that pass verification, it also reports names that share the same email and organization. Duplicate identity is a warning rather than a failure because it can be intentional.

`--ignore-pin` bypasses the identity comparison for one launch only. It prints
the account Claude reports before launching, even when that account matches the
saved pin. It does not change the login, the saved pin, or the selected config
directory. Without the flag, the selected profile's pin remains mandatory.

## Limits

- A profile's pinned identity proves what Claude Code reported when you registered or refreshed it. It cannot establish that you chose the right account at that moment. Inspect the output from `add`, `refresh`, and `check`.
- The `cc` function is for interactive Bash shells. Scripts can call `ccenv run -- ...` directly. GUI integrations that invoke `claude` themselves do not automatically use `ccenv`.
- `ccenv` selects accounts at process launch. It does not switch a running Claude session or rotate accounts on usage limits.
- Profile directories isolate user-level data as supported by `CLAUDE_CONFIG_DIR`; project and managed settings may still apply to the same working directory.
