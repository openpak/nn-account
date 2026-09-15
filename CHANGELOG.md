# Changelog — nn-account

Generated from git history on 2026-09-15. `git log` stays the source
of truth; this file is the readable summary.

Note: the early history below is the upstream project (Pretendo Juxt-Server etc.);
OpenPak work starts at the port/fork commit.

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
