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

## Install

Download the archive for your OS and CPU, plus `SHA256SUMS`, from [Releases](https://github.com/JinHo-von-Choi/anti-samcheonpo/releases/latest). Archives are provided for Linux, macOS, and Windows on amd64 and arm64.

Linux x86-64 example:

```sh
sha256sum --ignore-missing -c SHA256SUMS
tar -xzf samcheonpo_0.12.0_linux_amd64.tar.gz
mkdir -p ~/.local/bin
cp samcheonpo samcheonpo-hook ~/.local/bin/
export PATH="$HOME/.local/bin:$PATH"
samcheonpo --version
```

Keep both executables in the **same directory** and add it to your shell's persistent PATH. On macOS, download the `darwin` archive and check it with `shasum -a 256 -c SHA256SUMS --ignore-missing`. On Windows, extract both `.exe` files to one directory on PATH and install Git for Windows for shell checks.

To install from source, use Go 1.27 or later and install both commands:

```sh
go install github.com/JinHo-von-Choi/anti-samcheonpo/cmd/samcheonpo@v0.12.0
go install github.com/JinHo-von-Choi/anti-samcheonpo/cmd/samcheonpo-hook@v0.12.0
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

Antigravity CLI (`agy`), MiniMax Code, GitHub Copilot and other agents are also supported. See [connection instructions](docs/getting-started.md#에이전트-연결-확인) and the [support matrix](docs/support-matrix.md). For Python applications, see the [Deep Agents integration](plugins/deepagents/README.md).

**3. Work normally and respond to warnings.** Run these in your own terminal, in the project directory:

| Need | Command |
| --- | --- |
| Review current status | `samcheonpo cmd summary` |
| Reject an incorrect finding | `samcheonpo cmd keep normal` |
| Allow this occurrence | `samcheonpo cmd keep now` |
| Send the suggested next step to the agent | `samcheonpo cmd steer` |

Claude Code also supports slash commands such as `/samcheonpo:summary` and `/samcheonpo:keep normal`. Codex uses the terminal commands above.

## When does it block work?

The default mode advises first. Selected rules can block a repeat involving the same goal and target, up to three escalated blocks per session. Separately, ending a turn after a code change with no check holds the turn end; the same change is held at most twice, then the turn ends as unverified and you are told. Other rules only record findings. Accepted protected paths and budgets are enforced separately.

To record ordinary findings without advice, merge this into `~/.samcheonpo/config.yml` and start a new session:

```yaml
rollout:
  mode: shadow
```

Accepted protected paths, budgets and core pre-tool guards such as error hiding and test weakening still apply in shadow mode. See [blocking rules](docs/reference.md#귀띔에서-차단까지).

To disconnect, uninstall the integration and start a new agent session:

```sh
samcheonpo uninstall --agent codex
```

Records and user-modified configuration are preserved. See the [upgrade guide](docs/getting-started.md#기존-버전에서-업그레이드) when replacing an existing installation.

## Review session records

```sh
samcheonpo receipt <session-id>
samcheonpo evidence --session <session-id>
samcheonpo interventions --session <session-id> --json
```

The receipt shows usage, `evidence` shows requirements and check results, and `interventions` shows advice and blocks. Currency values estimate token usage at API prices.

Records stay local by default. External judging and notifications are optional settings.

In-session messages follow the language you write in (Korean or English; other languages get English). Amounts are shown in dollars in English sessions.

## Documentation

Detailed guides are currently in Korean:

- [Usage guide](docs/getting-started.md): setup, troubleshooting, upgrades
- [Configuration and behavior](docs/reference.md): completion checks, limits, rollback, privacy
- [Repeated work and completion checks](docs/agent-failure-controls.md): requirements, artifacts, subagents
- [Support matrix](docs/support-matrix.md): agent and platform features
- [Development and evaluation](docs/development.md): builds, checks, experiments, releases
- [Changelog](CHANGELOG.md)

Copyright (c) 2026 Jinho Von Choi (최진호). Licensed under the [GNU Affero General Public License v3.0](LICENSE).
