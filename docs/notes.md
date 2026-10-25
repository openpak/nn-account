# Notes: nn-account

Go rewrite of Pretendo/account's Nintendo surface, rebuilt as a console
adapter over the OpenPak [account core](../../../../../platforms/core/infra/account). Serves NNAS HTTP and the
Pretendo-compatible `account.v2.AccountService` gRPC consumed by nn-friends.

**Status: M2 and M3 complete, M4 next** — PRD and M0 inventory: [PRD.md](../PRD.md), [docs/M0-INVENTORY.md](M0-INVENTORY.md).

## Deferred to M4

Console email-confirm trio, `/@me` profile update, and certificate signature crypto with
operator keys (reasons in `docs/M0-INVENTORY.md`). Everything a pinned Wii U/3DS client needs
for register, sign-in, NEX token and identity exchange runs in Go. The TypeScript tree under
`src/` is the upstream reference kept for provenance; the container image builds only
`cmd/nn-account`. No console or emulator has been run against this yet
(`docs/client-testing.md`).

## License

AGPL-3.0. Derived from Pretendo/account (AGPL-3.0); see M0 inventory for
per-file provenance.
