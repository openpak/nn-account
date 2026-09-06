# Client testing: prerequisites and configurations

PRD §13.2 requires selecting Wii U and 3DS emulator/hardware configurations
before M2 acceptance. This document records the planned matrix and current
status. **No console/hardware integration has been tested yet** — everything
below is a plan, not a result (PRD §10: untested paths are reported as such).

## Protocol-level status (automated)

Verified by the integration suites against the real Go core + adapter:

- Wii U path: NNAS registration, OAuth password/refresh grants (transformed
  credential via core), provider nex_token/service_token, gRPC v2 identity
  exchange with audience checks, ban/deletion enforcement
- 3DS path: NASC LOGIN/SVCLOC, device-only registration (provisional NEX
  identity), CTR system type on exchange, serial/MAC/cert negatives
- conntest + CBVC check pages

## Wii U configurations (planned)

| Client | Config | Status |
|---|---|---|
| Cemu (emulator) | Online mode with DNS redirection of `account.nintendo.net` to the adapter; account settings applet exercised via service-token flow | **untested** — needs Cemu build + online files; DNS override + cert trust to be documented at acceptance |
| Wii U hardware | Custom DNS + CA install (CFW); NNID environment patched to the adapter hostname | **untested** — requires console + CFW prerequisites; track separately |

Prerequisites to record at acceptance: exact Cemu version, online-files
source, patches applied, adapter DNS names, capture of a failing handshake
if any (PRD §10 evidence requirements).

## 3DS configurations (planned)

| Client | Config | Status |
|---|---|---|
| Citra (emulator) | NNAS/NASC hosts redirected; friend-code + account creation flow via NASC `0004013000003202` | **untested** — needs Citra online support for NASC; confirm which Citra builds send `fcdcert` |
| 3DS hardware (CFW) | NintendoWFC patches / NNID redirect; console cert chain trusted by adapter | **untested** — hardware + CFW required |

Known deferred dependency: full cryptographic verification of `fcdcert`
against LFCS/CA keys. Emulator flows often send synthetic certificates;
the adapter currently performs structural validation only. If hardware
testing is attempted, operator-supplied keys are required (see
`internal/cert`), and this must be resolved first.

## nn-friends contract client

- Pinned `nn-friends` at `eb85ca3` with the adapter's `account.v2` gRPC:
  automated coverage exists for `GetUserData`, `GetNEXPassword`,
  `ExchangeNEXTokenForUserData` (audience + type checks). A live NEX
  connection test (friends server + console login) is **M2 acceptance
  work**, pending the emulator configs above.
