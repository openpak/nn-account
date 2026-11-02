# Client testing: prerequisites and configurations

PRD §13.2 requires selecting Wii U and 3DS emulator/hardware configurations.
This document records the client matrix and its prerequisites.

## Protocol-level coverage (automated)

Covered by the integration suites against the real Go core + adapter:

- Wii U path: NNAS registration, OAuth password/refresh grants (transformed
  credential via core), provider nex_token/service_token, gRPC v2 identity
  exchange with audience checks, ban/deletion enforcement
- 3DS path: NASC LOGIN/SVCLOC, device-only registration (provisional NEX
  identity), CTR system type on exchange, serial/MAC/cert negatives
- conntest + CBVC check pages

## Wii U configurations

| Client | Config | Needs |
|---|---|---|
| Cemu (emulator) | Online mode with DNS redirection of `account.nintendo.net` to the adapter; account settings applet exercised via service-token flow | needs Cemu build + online files; DNS override + cert trust to be documented at acceptance |
| Wii U hardware | Custom DNS + CA install (CFW); NNID environment patched to the adapter hostname | requires console + CFW prerequisites; track separately |

Prerequisites to record: exact Cemu version, online-files
source, patches applied, adapter DNS names, capture of a failing handshake
if any (PRD §10 evidence requirements).

## 3DS configurations

| Client | Config | Needs |
|---|---|---|
| Citra (emulator) | NNAS/NASC hosts redirected; friend-code + account creation flow via NASC `0004013000003202` | needs Citra online support for NASC; confirm which Citra builds send `fcdcert` |
| 3DS hardware (CFW) | NintendoWFC patches / NNID redirect; console cert chain trusted by adapter | hardware + CFW required |

Known deferred dependency: full cryptographic verification of `fcdcert`
against LFCS/CA keys. Emulator flows often send synthetic certificates;
the adapter currently performs structural validation only. Hardware
testing needs operator-supplied keys (see `internal/cert`).

## nn-friends contract client

- Pinned `nn-friends` at `eb85ca3` with the adapter's `account.v2` gRPC:
  automated coverage exists for `GetUserData`, `GetNEXPassword`,
  `ExchangeNEXTokenForUserData` (audience + type checks). A live NEX
  connection test needs a friends server and a console or emulator login.
