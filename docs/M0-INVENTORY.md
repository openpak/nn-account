# M0: Port inventory and ownership catalog

Source: Pretendo/account `f7b1bc2509452b9e97dad592f255dbe85d674200`, friends `eb85ca351ddc469859974d45bf74989c7260e3d1`.
Statuses: **required** (account v1), **deferred** (later milestone), **excluded** (not built).

## 1. HTTP routes

### NNAS → `nn-account`

| Route | Disposition | Consumer | Notes |
|---|---|---|---|
| POST `/v1/api/oauth20/access_token/generate` | required | Wii U/3DS login | Console auth; separate from third-party OAuth. Token types, audiences per FR-3 |
| POST `/v1/api/people` | required | 3DS/Wii U registration | Creates PNID; delegates identity policy to core |
| GET `/v1/api/people/:username` | required | console | Duplicate-check during registration |
| GET `/v1/api/people/@me/profile` | required | console | Profile projection; minimize fields |
| GET `/v1/api/people/@me/devices` | required | console | Device bindings |
| POST `/v1/api/people/@me/devices` | required | console | Device binding |
| GET `/v1/api/people/@me/devices/owner` | required | console | |
| GET `/v1/api/people/@me/devices/status` | required | console | |
| PUT `/v1/api/people/@me/devices/@current/inactivate` | required | console | |
| PUT `/v1/api/people/@me/miis/@primary` | required | console | Mii storage in adapter |
| PUT `/v1/api/people/@me` | required | console | Profile update |
| POST `/v1/api/people/@me/deletion` | required | console | Delegates to core delete |
| GET/PUT `/v1/api/people/@me/emails(/@primary)` | required | console | Email change; verify via core |
| GET `/v1/api/provider/service_token/@me` | required | console | Audience-validated service tokens |
| GET `/v1/api/provider/nex_token/@me` | required | console, nn-friends | NEX token issuance; enforce type/audience (upstream TODO) |
| GET `/v1/api/admin/mapped_ids` | required | internal | |
| GET `/v1/api/admin/time` | required | console | |
| GET `/v1/api/content/agreements/:type/:region/:version` | required | console | OpenPak-authored content |
| GET `/v1/api/content/time_zones/:countryCode/:language` | required | console | Static data |
| GET `/v1/api/support/validate/email` | required | console | |
| PUT `/v1/api/support/email_confirmation/:pid/:code` | required | console | Delegates to core verification |
| GET `/v1/api/support/resend_confirmation` | required | console | |
| GET `/v1/api/support/send_confirmation/pin/:email` | required | console | |
| GET `/v1/api/support/forgotten_password/:pid` | required | console | Delegates to core recovery |
| GET `/v1/api/account_settings/ui/profile` | required | browser | Embedded settings page |
| POST `/v1/api/account_settings/update` | required | browser | Delegates shared changes to core |
| GET `/v1/api/account_settings/mii/:pid/:face` | required | browser | Mii asset |

### NASC → `nn-account`

| Route | Disposition | Notes |
|---|---|---|
| POST `/ac` (`LOGIN`, `SVCLOC` actions) | required — Go | Go: internal/nasc. Middleware port incl. device-only registration (FR-2 provisional identities), MAC OUI list, serial checks. Cert signature crypto deferred (operator keys) |

### Web API → `account` (new OpenPak API replaces upstream)

| Upstream route | Disposition | Notes |
|---|---|---|
| POST `/v1/api/register` | required | Core-owned web registration |
| POST `/v1/api/login` | required | |
| GET `/v1/api/email/verify` | required | |
| POST `/v1/api/forgotPassword` | required | |
| POST `/v1/api/resetPassword` | required | |
| GET/POST `/v1/api/user` | required | Profile view/update via core web session |

### Other upstream services

| Surface | Disposition | Notes |
|---|---|---|
| conntest (`/` POST) | required | Client dependency for console connect checks |
| cbvc | required (document unsupported subroutes) | Determine client dependency |
| healthz | required | Per service |
| local-cdn `GET /*` | required | User-owned profile/Mii assets with limits |
| datastore `/upload` | excluded | PRD §5 exclusion |
| assets (static) | required | Settings page assets |
| account-settings EJS pages | required | Go templates in adapter |

## 2. gRPC

### Account v2 (`nn-account` serves; pinned nn-friends client)

| RPC | Disposition | Notes |
|---|---|---|
| `GetUserData` | required | friends; minimize email/device/permission fields with consumer evidence |
| `GetNEXPassword` | required | friends |
| `ExchangeNEXTokenForUserData` | required | friends; game-server id `00003200`; enforce audience/type checks |
| `GetPNID`, `GetPNIDs`, `ListPNIDs` | deferred → M3 | moderation/console tooling |
| `GetNEXAccount`, `ListNEXAccounts`, `UpdateNEXAccount` | deferred → M3 | |
| `GetDevice`, `ListDevices`, `UpdateDevice` | deferred → M3 | |
| `ExchangeIndependentServiceTokenForUserData`, `ValidateIndependentServiceToken` | deferred | only if a consumer appears |
| `ExchangeOAuthTokenForUserData` | excluded | third-party OAuth lives in core, different contract |
| `ExchangePasswordResetTokenForUserData` | deferred | adapter flow only if console needs it |
| `ExchangeTokenForUserData` (v2) | deferred | legacy |
| Bans/audit/servers CRUD (`IssueBan`, `PardonBan`, `GetBan`, `UpdateBan`, `ListBans`, ban/audit comments, `CreateServer`…`UpdateServer`) | deferred → M3/M4 | core moderation owns policy; adapter implements contract against core |
| `DeleteAccount`, `DeletePNID`, `UpdatePNID`, `UpdatePNIDPermissions` | deferred → M3 | |

### Account v1 → excluded unless an in-scope consumer requires it (inventory says none; friends uses v2).

### API v1/v2 (web register/login/etc. over gRPC for the website)

| RPC | Disposition | Notes |
|---|---|---|
| Register, Login, ForgotPassword, ResetPassword, VerifyEmail, UpdateEmail, GetUserData, UpdateUserData | required (core implements; adapter v1/v2 contracts only if website consumers exist) | Website becomes core's Go web UI; gRPC API service deferred unless a consumer exists |
| SetDiscordConnectionData, SetStripeConnectionData | excluded | PRD §5 |

## 3. Core ↔ adapter contracts (protobuf, defined in `account` repo)

1. `IdentityVerification` — verify email+password, account status (active/banned/deleted), minimal profile; no hash retrieval.
2. `LinkLifecycle` — reserve/activate/unlink opaque (namespace, subject_id); idempotency keys; states `pending → active → revoking → revoked`.
3. `GraphOperations` — canonical friend request/accept/remove/block using core account IDs + actor authorization.
4. `InvalidationEvents` — durable, versioned: ban, unban, unlink, delete, credential revocation.
5. `PrivacyParticipation` — scoped export, acknowledged cleanup with retry.

Failure behavior: cross-service link activation is two-phase via states above; adapter issuance/validation fails closed (retryable error) when core is unreachable.

## 4. License provenance

- Fork root LICENSE: AGPL-3.0 text; `package.json` declares ISC (metadata discrepancy noted in PRD §11 — resolve before relicensing claims; keep AGPL notices).
- Derived Go ports record: source path + upstream revision in file header comment.
- Protobuf dependencies pinned at upstream versions (`@pretendonetwork/grpc` v2 protos) in adapter only.

## 5. First client targets

- Wii U: console + emulator (Cemu) — config recorded at M2 acceptance.
- 3DS: console + emulator (Citra) — config recorded at M2 acceptance.
- nn-friends `eb85ca3` pinned for gRPC v2 contract tests.
