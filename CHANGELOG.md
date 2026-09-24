# Changelog — nn-account

Generated from git history on 2026-09-15. `git log` stays the source
of truth; this file is the readable summary.

Note: the early history below is the upstream project (Pretendo Juxt-Server etc.);
OpenPak work starts at the port/fork commit.

## v0.8.1 — an emulator sign-in links its own family

- Signing an account into Azahar or Cemu makes sure the account has an active link in that
  family (`EnsureLink`). An account whose NNID was made on a Wii U had only a "wiiu" link, so
  its 3DS friend code on Azahar never reached the website or the app; it now gets a "3ds"
  link on the first Azahar sign-in, and the reverse for Cemu.
- A Cemu sign-in publishes the NNID straight away instead of at the next restart.

## v0.8.0 — NNIDs on the Wii U link

- The Nintendo Network ID is published on the account's Wii U link as its public code
  (`Links.SetLinkPublicCode`, account `docs/public-codes.md`): at registration, and for every
  existing PNID in a pass at startup (idempotent; it also repairs a registration whose publish
  failed). The website and the app show it in the profile's friend codes, and the website's
  add-friend dialog resolves a Wii U friend by it (website v0.56.0).
- `internal/accountpb` refreshed from the core's `account/proto/openpak/account/v1`.

## v0.7.0

- Bans (website/docs/ban-lookup.md): the two v2 RPCs that still handed out credentials for a
  banned owner now refuse it. `GetNEXData` checks the owning account's core status like
  `GetNEXPassword` (InvalidArgument "Account is banned or deleted"), and
  `ValidateIndependentServiceToken` answers `is_valid: false` for a banned or deleting owner.
  The NEX titles map the InvalidArgument "banned" refusal to `RendezVous::AccountDisabled`;
  the integration journey now pins it for both RPCs. Everything else already enforced the
  ban: NNAS sign-in, refresh and every bearer call answer 0108 "Account has been banned",
  NASC answers 102, and `account_banned` revokes local tokens. No new environment variables.

## v0.6.0 — NEX tokens know their client

- NEX tokens record the client they were issued to: `X-OpenPak-Client` from Cemu (NNAS) or
  Azahar (NASC LOGIN), else `wiiu`/`3ds` (migration 0005, `nex_tokens.client`).
  `Resolution.ResolveNexTokenClient` lets nn-friends publish "Cemu"/"Azahar" vs the console.
- `/internal/resolve/{pid,account}`: the Resolution mapping as JSON under `X-Internal-Key`,
  for the TypeScript Miiverse bridge.
- Integration tests cover header -> token -> client on both surfaces; the NASC journey reads
  response fields by name.

## v0.5.0 — 2026-09-13

- Emulator surface: the identity carries the console password cache (NA-1a) and the PNID's
  country and language. gofmt.

## v0.4.0 — 2026-09-11



## v0.3.0 — 2026-09-10



## v0.2.0 — 2026-09-10



## v0.1.0 — 2026-09-10



## v0.1.0 — 2026-09-10

- Go 1.26, the house version [9a1b8a3]
- Ship the Go adapter in the release image, not the legacy Node runtime [e135128]
- Release workflow like every other OpenPak service: tag → ghcr, amd64 and arm64 [c054a19]
- fix(security review round 2): close 10 adapter findings [8edab24]
- docs: PRD status — M3 core integration landed [a3e3450]
- feat(M3): Resolution gRPC service — PID <-> core-account mapping for friends [a81e8b9]
- docs: client-testing prerequisites and M2 status [5572ead]
- fix: mount nnas at root so conntest/cbvc resolve; verified via two-binary live smoke [3518ee2]
- feat(M2): conntest/CBVC checks, Wii U settings applet in Go [45d3b83]
- feat(M2): devices/miis/mapped_ids/content routes, console deletion via core [09b7b51]
- feat(M2): NASC /ac in Go — LOGIN/SVCLOC, device-only registration, cert parser [18375af]
- docs: PRD status update (M1 complete, M2 in progress) [2449633]
- feat(M2): Go Nintendo adapter — gRPC v2 contract, NNAS core routes, core delegation [f3743be]
- docs: M0 port inventory and ownership catalog (PRD milestone M0) [f3e7108]
- Merge pull request #358 from PretendoNetwork/feat/healthcheck-metrics-and-more [f7b1bc2]
- chore: add documentation of new config fields [4bcfb7b]
- fix: Remove deprecated usage of request.host [e4d8950]
- feat: implement PNID and NEXToken metrics [7babb41]
- feat: add support for prometheus metrics [8a1964f]
- fix: made error logs include the stacktrace [5b3b5e4]
- feat: add healthcheck endpoint [a622772]
- fix: remove deletion workflow from boot [ba245c0]
- chore: Made schedule creation a success log and not an error [13b6feb]
- Merge pull request #355 from PretendoNetwork/feat/update-email [34dc075]
- fix: type import [4e19a2f]
- fix: clear email history... better? [5cc6dd3]
- fix: use v2 auth middleware for v2 paths [dfe17a2]
- fix: history not tracked on nnas endpoint, not scrubbed [7f9d91b]
- Merge pull request #356 from PretendoNetwork/chore/mongo-device-index [990c9bf]
- chore: add Mongo index on linked_pids in DeviceSchema [394e772]
- feat: add updateEmail/verifyEmail grpc, track email changes [0bb3d86]
- Merge pull request #354 from PretendoNetwork/feat/date-validation [2f93854]
- feat: account birthday validation [07d2809]
- Merge pull request #351 from PretendoNetwork/feat-account-edit [81cbacf]
- lint: run linter [11f10aa]
- chore: update eslint rules [45ac3b8]
- feat: simplify region update logic, per review [cbf2fdf]
- fix: update user data endpoint based on review [770c706]
- feat: add account editing [d6791f1]
- fix: pnid interface [0c8f4a0]

- … 720 earlier commits omitted (see `git log`)
