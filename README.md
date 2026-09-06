# OpenPak nn-account — Wii U/3DS adapter (Go)

Go rewrite of Pretendo/account's Nintendo surface, rebuilt as a console
adapter over the OpenPak [account core](../account). Serves NNAS HTTP and the
Pretendo-compatible `account.v2.AccountService` gRPC consumed by nn-friends.

**Status: M2 in progress** — PRD and M0 inventory: [PRD.md](PRD.md),
[docs/M0-INVENTORY.md](docs/M0-INVENTORY.md).

## What runs in Go

- NNAS: `/v1/api/oauth20/access_token/generate`, `/v1/api/provider/{nex_token,service_token}/@me`,
  `POST /v1/api/people` (registration), people lookup, `admin/time`,
  `support/validate/email`
- gRPC v2: `GetUserData`, `GetNEXPassword`, `ExchangeNEXTokenForUserData`
  (with the PRD-mandated game-server audience + token-type checks upstream lacked)
- Console credentials: transformed console secrets are verified by the core's
  adapter-credential domain; no password material is stored or verified here
- Durable invalidation events from the core revoke local tokens (bans/unlinks)

## Still TypeScript (until M2 completes)

NASC `/ac`, devices/miis/support/admin route subset, settings pages, assets,
conntest/CBVC. The legacy runtime stays until each surface is replaced.

## Run

```sh
cp example.env .env   # fill in required values
set -a; source .env; set +a
go run ./cmd/nn-account
```

Requires the account core running and PostgreSQL.

## Tests

```sh
docker run -d --name openpak-pg-test -e POSTGRES_PASSWORD=test \
  -e POSTGRES_DB=account_test -p 54329:5432 postgres:18-alpine
go test -tags integration -race -count=1 ./...
```

The integration suite builds the real core binary and exercises the full
console journey: register → sign in → NEX token → identity exchange, plus
fail-closed, audience-mismatch, and ban negative scenarios.

## License

AGPL-3.0. Derived from Pretendo/account (AGPL-3.0); see M0 inventory for
per-file provenance.
