# Developer conventions

How Goblin Gate is put together and the rules code follows. Read this before a pull request.

## Dev environment

- Development happens on **Windows with PowerShell**; the product runs on **Linux** (Debian 12).
- Code that touches the OS (netlink, nft, systemctl, files under /etc) goes in files with
  `//go:build linux` and has a fake or no-op version for other OSes (`//go:build !linux`), so Windows
  builds and tests still work.
- `scripts/verify.ps1` builds for Windows and Linux, runs vet, gofmt and the Go tests, and lints, builds
  and tests the web UI. CI runs the same script.
- Linux behavior is tested in Docker (`scripts/linux-test.ps1`, `scripts/linux-e2e.ps1`) and on real VMs
  (`scripts/vm-install-test.ps1`, `scripts/vm-net-test.ps1`).

## Architecture

```
Web GUI ─┐
CLI     ─┼─► REST API (goblind) ─► config tree (candidate / running / history)
REST    ─┘                              │ commit
                                        ▼
                          renderers (pure functions: config → text files)
                                        ▼
                          applier (linux only: write files, nft -f, reload services)
                                        ▼
      data plane: nftables · systemd-networkd · Kea DHCP · Unbound · Suricata · WireGuard
                                        │ logs / events
                                        ▼
                          log store (SQLite) ─► stats API ─► dashboards
```

- **One config tree** is the single source of truth. The GUI and CLI both edit the *candidate* through
  the API. A commit validates, renders, applies and saves a history revision; `commit confirm N` rolls
  back automatically unless confirmed.
- **Renderers are pure functions** (`func Render(cfg *config.Config) (map[string][]byte, error)`). They
  never touch the OS and are tested with golden files in `testdata/`.
- The **applier** checks every file before writing (`nft -c`, `unbound-checkconf`, `kea-dhcp4 -t`), backs up
  the previous generation and rolls back if activation fails.
- **Policies** match traffic and attach named **security profiles** (IPS, web filter, DNS filter, app
  control, threat feeds). Profiles are defined once and reused.
- Deeper design notes: [nftables.md](nftables.md).

## Tech stack

Go 1.26, module `github.com/networkgoblinlabs/goblin-gate`. Prefer the standard library (`net/http` route
patterns, `log/slog`, `encoding/json`, `net/netip`). Allowed extras: `modernc.org/sqlite` (pure Go),
`golang.org/x/crypto`, `golang.org/x/term`, `github.com/google/go-cmp` (tests). New dependencies need a good
reason in the pull request.

Web UI (`web/`): Vite, React, TypeScript (strict), Tailwind, `@tanstack/react-query`, `react-router-dom`,
`echarts`, `react-grid-layout`, `@dnd-kit`, `lucide-react`; tests with Vitest and Testing Library.

## Repo layout

```
cmd/goblind/          management daemon (API server, commit engine, log ingest)
cmd/goblin/           CLI client (FortiOS-style shell)
cmd/goblin-console/   console menu on the box (tty1)
internal/config/      schema structs, defaults, validation, store, diff
internal/commit/      commit engine, confirm timer, rollback
internal/api/         HTTP handlers, auth middleware
internal/render/      one sub-package per engine: netif, nft, kea, unbound, suricata, wireguard, ...
internal/apply/       applier (linux implementation and fake)
internal/feeds/       threat feed fetch, parse, cache
internal/logs/        log ingest, SQLite store, stats queries
internal/cli/         CLI parser and shell
web/                  React app (served by goblind)
packaging/            deb, ISO profiles, systemd units, default config
scripts/              verify, build, Linux and VM tests
docs/                 user and developer docs
```

## Code style

- Go: `gofmt` clean, small files (under about 400 lines), errors wrapped with context
  (`fmt.Errorf("render nft: %w", err)`), no panics in library code, no global state outside `main`.
  Table-driven tests. Exported identifiers have doc comments.
- Config JSON uses `snake_case` keys. Object names (addresses, policies, profiles) are unique, 1-63
  characters of `[A-Za-z0-9_.-]`.
- TypeScript: strict, no `any`; components in `web/src/components`, pages in `web/src/pages`, API calls only
  through `web/src/api/`.
- Web pages use the **real API only**, never mock data in a page. Config lists are edited with
  `web/src/components/ConfigList.tsx` and the calls in `web/src/api/objects.ts` (see
  `pages/network/Addresses.tsx`). Tests mock the API module with `vi.mock`.
- UI: dark (default) and light themes from `tailwind.config.ts`; check visual changes with screenshots,
  since unit tests cannot see styling.
- Security: never log passwords or tokens, validate all API input, default deny.
