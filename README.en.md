<div align="center">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.png">
  <img src="docs/assets/logo.png" alt="Samcheonpo (anti-samcheonpo)" width="260">
</picture>

**A local monitor that records repeated work and failures in coding agents, provides advice, and can block selected actions.**

[CI](https://github.com/JinHo-von-Choi/anti-samcheonpo/actions/workflows/ci.yml) · [Download](https://github.com/JinHo-von-Choi/anti-samcheonpo/releases/latest) · [한국어](README.md)
</div>

## What is it for?

Connect Samcheonpo to Claude Code or Codex to observe repeated checks, repeated errors, and edits outside the requested scope. It uses commands, file changes, and execution results to suggest a next step. Selected repeated actions can be blocked before execution. You can also inspect usage and findings from previous sessions.

**Improved completion rates and cost savings have not been established.** Use it to observe work and decide when to intervene. The [16-run Codex pilot](docs/benchmarks/2026-10-21-codex-pilot.md) did not establish a benefit. Samcheonpo does not write code for you or certify that an agent's work is correct.

## Install

Download the archive for your OS and CPU, plus `SHA256SUMS`, from [Releases](https://github.com/JinHo-von-Choi/anti-samcheonpo/releases/latest). Archives are provided for Linux, macOS, and Windows on amd64 and arm64.

Linux x86-64 example:

```sh
sha256sum --ignore-missing -c SHA256SUMS
tar -xzf samcheonpo_0.9.11_linux_amd64.tar.gz
mkdir -p ~/.local/bin
cp samcheonpo samcheonpo-hook ~/.local/bin/
export PATH="$HOME/.local/bin:$PATH"
samcheonpo --version
```

Keep both executables in the **same directory** and add it to your shell's persistent PATH. On macOS, download the `darwin` archive and check it with `shasum -a 256 -c SHA256SUMS --ignore-missing`. On Windows, extract both `.exe` files to one directory on PATH and install Git for Windows for shell checks.

To install from source, use Go 1.27 or later and install both commands:

```sh
go install github.com/JinHo-von-Choi/anti-samcheonpo/cmd/samcheonpo@v0.9.11
go install github.com/JinHo-von-Choi/anti-samcheonpo/cmd/samcheonpo-hook@v0.9.11
```

## Start using it

**1. Inspect previous records.** No model call or agent connection is required. Analysis results are saved to a local ledger.

```sh
samcheonpo audit --agent codex --since 7d
samcheonpo sessions
```

**2. Connect your agent.** Choose the one you use:

```sh
samcheonpo install --agent codex
# For Claude Code: samcheonpo install --agent claude
samcheonpo doctor --agent codex
```

Start a **new agent session**. If the agent asks you to trust hooks or a plugin, review and approve the installed Samcheonpo entry. The first hook starts the monitor automatically.

Cursor CLI is observation-only. The current source supports generic tool hooks and connection checks; actual blocking and advice delivery remain unverified. See the [Cursor connection guide](docs/getting-started.md#cursor-cli에서-관찰하기).

GitHub Copilot CLI connects with `samcheonpo install --agent copilot`. Pre-tool blocking, post-tool advice, stop continuation and prompt advice were verified against the real CLI. See the [Copilot connection guide](docs/getting-started.md#github-copilot-cli에-연결하기).

**3. Work normally and respond to warnings.** Run these in your own terminal, in the project directory:

| Need | Command |
| --- | --- |
| Review current status | `samcheonpo cmd summary` |
| Reject an incorrect finding | `samcheonpo cmd keep normal` |
| Allow this occurrence | `samcheonpo cmd keep now` |
| Send the suggested next step to the agent | `samcheonpo cmd steer` |

Claude Code also supports slash commands such as `/samcheonpo:summary` and `/samcheonpo:keep normal`. Codex uses the terminal commands above.

## When does it block work?

The default mode advises first. Selected rules can block a repeat involving the same goal and target, up to three escalated blocks per session. Other rules only record findings. Accepted protected paths and budgets are enforced separately.

To start with recording only, merge this into `~/.samcheonpo/config.yml`, then start a new session:

```yaml
rollout:
  mode: shadow
```

Accepted protected paths and budgets still apply in shadow mode. To disconnect completely, uninstall the integration and start a new agent session:

```sh
samcheonpo uninstall --agent codex
```

Records and user-modified configuration are preserved. Before upgrading, stop active agent sessions and the monitor, back up `~/.samcheonpo/`, replace both executables, and uninstall/reinstall each integration. Ledger schema 12 cannot be opened by v0.8.0 or older.

## Read results correctly

- No warning does not mean the task is complete. Check the deliverable and its tests.
- Currency values estimate token usage at API prices. They are not invoices or savings. Missing usage or prices remain unknown.
- False positives are possible. `keep normal` releases a matching finding, but does not remove protected paths or budgets.
- Records stay local by default. External judging and sending are optional. The ledger and handoff files can contain paths and task text.

## Documentation

Detailed guides are currently in Korean:

- [Usage guide](docs/getting-started.md): setup, troubleshooting, upgrades
- [Configuration and behavior](docs/reference.md): completion checks, limits, rollback, privacy
- [Support matrix](docs/support-matrix.md): tested agents and platforms
- [Development and evaluation](docs/development.md): builds, checks, experiments, releases
- [Changelog](CHANGELOG.md)

[MIT License](LICENSE)
