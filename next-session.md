# Next session — nn-account
Updated 2026-09-24.

Pretendo's account server rewritten in Go as the Wii U/3DS adapter over the
OpenPak account core: NNAS HTTP, NASC `/ac`, and the `account.v2` gRPC that
nn-friends and the content services consume. Deployed 2026-09-10 behind
Traefik TLS (20050/20051), not console-verified — client builds exist
(`nn-inkay` Wii U, `nn-nimbus` 3DS on `openpak-v1` 2026-09-12) but no console
has signed in yet.

Current status 2026-09-24: latest tag v0.8.1 (c3b3d78), `dev` clean. Since
v0.6.0: v0.7.0 bans (`GetNEXData`/`ValidateIndependentServiceToken` refuse a
banned owner), v0.8.0 NNID published on the Wii U link, v0.8.1 emulator
sign-in links its own family; CI builds on `v*.*.*` tags only.

## Where things stand

- M0–M3 complete (PRD.md): shadow PNIDs, NNAS+NASC surfaces, conntest/CBVC,
  gRPC v2 with audience/type checks, Resolution service (PID <-> core account).
- v0.5.0 = e334770 (2026-09-13): the internal emulator surface carries
  the Wii U/3DS identity, the console password cache (NA-1a), and the PNID's
  country and language.
- v0.6.0 (2026-09-23, deployed): NEX tokens record the client they were
  issued to (`X-OpenPak-Client` from Cemu/Azahar, else `wiiu`/`3ds`;
  `nex_tokens.client`, migration 0005); `Resolution.ResolveNexTokenClient`
  lets nn-friends publish "on Cemu"/"on Azahar". Also `/internal/resolve/{pid,account}`
  (resolvehttp, the plain-HTTP Resolution face for the Miiverse bridge; no
  tests of its own yet).
- v0.7.0 (2026-09-23): the two v2 RPCs that still handed out credentials to a
  banned owner (`GetNEXData`, `ValidateIndependentServiceToken`) refuse it.
- v0.8.0/v0.8.1 (2026-09-24): the NNID is the Wii U link's public code (at
  registration + startup pass); an Azahar/Cemu sign-in ensures a `3ds`/`wiiu`
  link (`EnsureLink`) and a Cemu sign-in publishes the NNID at once.
- M4 deferred with recorded reasons (docs/M0-INVENTORY.md): console
  email-confirm trio, `/@me` profile update, cert signature crypto with
  operator keys.
- `src/` is the upstream TypeScript tree (Pretendo account/Juxt) kept for
  provenance; the container image builds only `cmd/nn-account`.
- Integration suite runs the real core + Postgres through the full console
  journey plus ban/audience negatives: `go test -tags integration -race ./...`.

## Open questions

- Cert signature crypto (M4): which operator keys, and must hardware
  `fcdcert` verification land before the first console sign-in
  (docs/client-testing.md flags it as a hardware-testing dependency)?
- Cemu online-files source and the DNS/cert-trust recipe for the planned
  client matrix — still unrecorded.

## Next steps

1. Tests for `internal/resolvehttp` once the Miiverse bridge's needs settle.
2. M4 in PRD order: email-confirm trio, `/@me` profile update, cert crypto.
3. Console verification: first sign-in via nn-inkay or nn-nimbus against prod;
   record the client matrix at acceptance (docs/client-testing.md).

## Pointers

- PRD.md, docs/M0-INVENTORY.md, docs/client-testing.md
- ../prds/platform-wiiu-prd.md, ../prds/platform-3ds-prd.md
- ../account (core), ../nn-friends (gRPC consumer), ../nn-juxtaposition
  (resolvehttp consumer)

## Scratch (research and throwaway work)

Decompiles, Ghidra projects, dumps, exefs/romfs extracts, packet captures,
strace and emulator logs, probe harnesses: put them in
`~/REPOS/Openpak/scratch/<topic>`. That folder is a local mount of the media pool,
outside every repository, so nothing in it is committed. Never use `/tmp` (a
shared 15 GB RAM disk) or elsewhere on `/home` for this. Keys and signing
material never go there. Rule: `docs/playbooks/conventions.md` in the workspace.
