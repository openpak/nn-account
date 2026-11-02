# Protocol: nn-account

## What runs in Go

- NNAS: `/v1/api/oauth20/access_token/generate`, `/v1/api/provider/{nex_token,service_token}/@me`,
  `POST /v1/api/people` (registration), people lookup, `admin/time`,
  `support/validate/email`
- gRPC v2: `GetUserData`, `GetNEXPassword`, `ExchangeNEXTokenForUserData`
  (with the PRD-mandated game-server audience + token-type checks upstream lacked)
- Console credentials: transformed console secrets are verified by the core's
  adapter-credential domain; no password material is stored or verified here
- Durable invalidation events from the core revoke local tokens (bans/unlinks)

## Bans

An account banned in the OpenPak admin is refused everywhere this adapter vouches for it
([`website/docs/ban-lookup.md`](../../../../../platforms/core/infra/website/docs/ban-lookup.md)): NNAS sign-in, token refresh and
every bearer call answer NNID error 0108 "Account has been banned"; NASC answers 102; the v2
gRPC (`GetNEXPassword`, `GetNEXData`, token exchanges, `GetUserData`) refuses with
InvalidArgument "Account is banned or deleted", which the NEX game servers turn into
`RendezVous::AccountDisabled`; `ValidateIndependentServiceToken` answers invalid. The core is
asked on every call (no cache), and `account_banned` revokes local tokens.

## Deferred (M4)

Console email-confirm trio, `/@me` profile update, and certificate signature crypto with
operator keys (reasons in `M0-INVENTORY.md`). Everything a pinned Wii U/3DS client needs for
register, sign-in, NEX token and identity exchange runs in Go. The TypeScript tree under
`src/` is the upstream reference; the image builds only `cmd/nn-account`. It serves NNAS HTTP
and the Pretendo-compatible `account.v2.AccountService` gRPC consumed by nn-friends.
