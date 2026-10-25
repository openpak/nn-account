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
