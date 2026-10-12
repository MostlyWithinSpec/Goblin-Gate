# Contributing to Goblin Gate

Thanks for helping build the firewall for people who want to understand and automate their network.

## Ways to help

- **Report bugs and request features** with the issue templates. Include the version (System → About or
  `goblind --version`) and what you expected.
- **Ask questions and share ideas** in Discussions.
- **Send pull requests.** For anything larger than a small fix, open an issue first so the approach can be
  agreed before you spend time on it.
- **Write plugins.** The plugin system is on the roadmap (see the roadmap on the project site); until it lands,
  ideas for what it should support are welcome in Discussions.

## Before you open a pull request

1. Read [docs/dev/conventions.md](docs/dev/conventions.md): architecture, stack, layout and code style.
2. Run the checks and make them pass:
   `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1`
   (Go 1.26 and Node 24; the same checks run in CI.)
3. Add tests for what you change. Web pages use the real API only, never mock data in the page.
4. Keep the change focused; describe what and why in the pull request.

Linux-only behavior is tested in Docker: `scripts/linux-test.ps1`, `scripts/linux-e2e.ps1`, and the VM tests
`scripts/vm-install-test.ps1` and `scripts/vm-net-test.ps1`.

## Contributor License Agreement

Goblin Gate is GPLv3. To keep the option of offering it under other terms later (for example, commercial
plugins or a commercial edition) while the core stays open, contributors sign a short CLA once: see
[CLA.md](CLA.md). A bot asks you on your first pull request; you sign by commenting on it.

## Code of conduct

Be respectful and constructive. Harassment or personal attacks are not tolerated in issues, pull requests or
Discussions.
