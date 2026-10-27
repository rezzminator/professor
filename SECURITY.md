# Security Policy

## Supported versions

Only the latest release receives security fixes. `develop` carries the next `-alpha`; a fix lands there and ships in the next release.

## Reporting a vulnerability

Report privately through GitHub's
[private vulnerability reporting](https://github.com/rezzminator/professor/security/advisories/new).
Do not open a public issue for a vulnerability.

Include the affected version (`pfm version`), the component, reproduction steps, and the impact you observed. You get an acknowledgement within 7 days and a fix-or-decline decision within 30.

## Scope

In scope: the `pfm` binary (installer, hooks, fleet engine, and MCP registration), the Harvester plugin at `plugins/harvester/`, the shipped templates under `templates/`, and the repository's GitHub Actions workflows.

Out of scope: vulnerabilities in Claude Code, Codex, OpenCode, or other third-party tools the framework drives — report those to their maintainers.
