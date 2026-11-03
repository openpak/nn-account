# Changelog — nn-account

Generated from git history on 2026-09-15. `git log` stays the source
of truth; this file is the readable summary.

Note: the early history below is the upstream project (Pretendo Juxt-Server etc.);
OpenPak work starts at the port/fork commit.

## v0.14.1 — 2026-11-03

- Integration test `TestAdapterConsoleJourney`: the availability check used `unregistered_name`, which the core's handle rule (6-15 letters and digits, starting with a letter, since the permanent core-owned handles) rejects with 0002; the code is right, the test was stale. It now checks a valid free name (empty 200) and that the underscore name is 400/0002. The default core checkout path follows the platforms/ layout (`ACCOUNT_CORE_DIR` still overrides).

## v0.14.0 — 2026-11-03

- Console password. `POST /internal/wiiu/console-password` (`X-Internal-Key`) `{account_id, password, mode}` sets the Wii U/3DS console credential to `NintendoPasswordHash(password, pid)`, in the core's credential domain and in `pnids.password_cache` as one value, so a real console's "Link an existing Network ID" and the emulator sign in with the same secret. It runs `Ensure` first, so an account with no PNID or a shadow one gets a full one. The plain password is never stored or logged.
  - `mode: "sync"` is the website handing over the OpenPak password at sign-in or password change, so the OpenPak password is the console password. A password a Wii U cannot take (outside 6-16 printable ASCII, or containing the Network ID) leaves a separate console password in place, otherwise the credential becomes random so a stale password stops working.
  - `mode: "custom"` (default) is a separate console password for that case; rule violations answer 422.
  - `GET /internal/wiiu/console-password?account_id=` answers `{nnid, pid, mode, min, max}` (`mode` is `sync`, `custom`, `incompatible` or empty). Migration 0007 adds `pnids.console_pw_mode`.
- Integration tests: `ACCOUNT_CORE_DIR` points at the core checkout.

## v0.13.0 — 2026-11-02

- `GET /internal/wiiu-nnid?account_id=<core account id>` (header `X-Internal-Key`, same key as `/internal/resolve/*`) answers `{"nnid": "...", "pid": 123}` for the account's Wii U name, or 404 when it has none (no PNID, deleted, a shadow, or no active core link for the PID, the same fail-closed rule as `ResolvePid`). OpenMaker's History claims use it to match an archived maker's Network ID to a signed-in account.

## v0.12.0 — 2026-11-01

- Mii images link to Nintendo's public Mii Studio renderer. The Mii URL in the account gRPC
  responses is now a `studio.mii.nintendo.com` link built from the stored `mii_data`
  (Wii U / 3DS Ver3 converted to Studio's 46-byte encoding, `internal/miistudio`, ported from
  PretendoNetwork/mii-js, AGPL-3.0-only). `GET /mii/{pid}/{file}` and the settings route
  `/v1/api/account_settings/mii/{pid}/{face}` answer 302 to Studio; the adapter never fetches,
  stores or caches the rendered image. Studio serves PNG only, so `.tga` requests get the PNG link.
- Fallback avatar: when `mii_data` is missing or invalid the same routes serve an original
  OpenPak SVG (favourite colour disc, cyan ring, initial) and `/mii/{pid}/fallback.svg` serves it
  explicitly. `NN_ACCOUNT_CDN_BASE_URL` is no longer used for Mii images.

## v0.11.0 — 2026-10-17

- provisioning: every active core account now has an identity and an active link in both the
  `wiiu` and `3ds` namespaces, whether or not the person ever used either console. A reconcile
  loop pages the core's `Identity.ListAccounts` at startup and every 60 s and runs the existing
  emulator `Ensure` for both families (idempotent; one failing account does not stop the pass;
  only counts are logged). `internal/accountpb` account stubs refreshed from the core v0.16.0.

## v0.10.0 — 2026-09-26

- playtime: a game server's login is the signal. After a successful `GetNEXPassword` (the v2
  gRPC every Wii U/3DS NEX title server calls when a player connects), the owning PNID's core
  account is sent to the core's `Sessions.MarkOnline` (title empty), which starts the playtime
  clock on the player's live titled session. The namespace is the NEX account's `device_type`
  (`wiiu` or `3ds`). Fire and forget on its own goroutine with a 5 s timeout, so the login is
  never slowed or failed by it; failures are logged at most once a minute. Device-only NEX
  records and failed lookups mark nobody. The core client gains a Sessions client on its
  existing connection. Vendored account proto refreshed (core v0.12.0: `MarkOnline`).
- The friends server asks for the password too, whenever a console connects; its calls carry
  `X-OpenPak-Caller: friends` (nn-friends v0.6.1) and mark nobody.

## v0.9.0 — 2026-09-26

- NEX tokens remember the OS an emulator runs on. `X-OpenPak-Client` may now carry it
  (`cemu/2.6 (linux)`); `clientid.ParseOS`/`OSOf` read windows, macos, linux, android or ios
  (anything else, or none, is ""). Both the NNAS `nex_token` and the NASC `LOGIN` store it
  next to the client (migration `0006_nex_token_os.sql`, additive, old rows ''), and
  `Resolution.ResolveNexTokenClient` returns it as `os` (field 4) for nn-friends to pass on
  with the core session. Vendored account proto refreshed (core v0.11.0: `os`, `GetPlaytime`).

## v0.8.3 — 2026-09-25

- resolution: an unknown PID answers found=false instead of a 500 (the Miiverse chat bridge and /internal/resolve/pid)

## v0.8.2 — 2026-09-24

- deps: grpc 1.83.2, x/crypto 0.57, x/net 0.59, pgx 5.11 (Dependabot)
- Removed the unused upstream package.json/package-lock.json (the Go server never used them)

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
