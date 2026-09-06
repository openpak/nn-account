# OpenPak Account Core and Nintendo Network Adapter: Go rewrite PRD

Status: in progress — M0 inventory complete (`docs/M0-INVENTORY.md`). M1 core
live in `../account` (identity, sessions, recovery, generic links, adapter
credential domain, invalidation events, internal gRPC, web API; race-tested).
M2 adapter well advanced: gRPC v2 contract, NNAS core routes (incl. devices,
miis, mapped_ids, content, deletion → core), and NASC /ac (LOGIN/SVCLOC,
device-only registration) run in Go with passing end-to-end Wii U + 3DS
journey suites; settings pages and the console email-confirm trio deferred
to M4 (recorded in the M0 inventory).

Owner: tobagin. Date: 2026-09-06.

Implementation targets: `openpak/account` (new console-agnostic core) and
`openpak/nn-account` (existing fork, rewritten as the Wii U/3DS adapter).
This cross-service PRD currently lives in the adapter fork; the core repository has
not yet been created. Document location does not imply ownership of core features.

## 1. Product objective

Replace Pretendo's TypeScript account server by separating its responsibilities into
two Go services. `account` becomes OpenPak's console-agnostic identity foundation;
`nn-account` preserves Wii U/3DS account protocols and integrates them with that core.
Switch, Xbox, and PlayStation attach through their own adapters to `account`, without
depending on `nn-account`, Nintendo-specific schemas, or another master account.

Users create one OpenPak account, connect their consoles, manage friendships and
privacy, and use supported online services. Operators can self-host the system without
a dependency on Pretendo's hosted services or a Node.js account runtime.

This is a port and redesign of appropriately licensed Pretendo functionality. Reuse
existing Go components; rewrite needed non-Go components in Go. OpenPak's Switch
implementation has no Nextendo code, service dependency, or affiliation.

## 2. Decisions and proposal boundaries

Confirmed by the project owner:

- One OpenPak identity across consoles, with separate OpenPak and Pretendo accounts.
- Separate `account` and `nn-account` from the start, with independent Go services and
  repository targets. The core owns generic identity and product rules; the adapter
  owns Wii U/3DS identities, device details, credentials, and wire protocols.
- Pretendo is the Nintendo foundation; OpenPak extends it with Switch support.
- All adopted Pretendo backend functionality must run in Go. A Go wrapper around a
  TypeScript service does not satisfy the requirement.
- Account-first delivery includes website access, console links, friends, OAuth,
  achievement/trophy capability, reporting, bans, and self-service export/delete.
- No game downloads, updates, DLC, or copyrighted vendor CDN assets.
- Self-hosting, EU reference hosting, minimal personal data, no analytics or telemetry.
- Full achievement/trophy parity is a recorded ambition. Its release meaning is still
  unresolved and must not be silently reduced to an empty database model.

Proposals in this PRD, not previously settled decisions:

- Use PostgreSQL for each service's persistence with separate ownership and credentials;
  do not carry MongoDB into the new runtime.
- Deliver an incremental internal compatibility milestone before the full account v1.
- Permit aggregate, operator-local service metrics without user tracking or external
  reporting. Confirm this interpretation of the no-telemetry policy before enabling them.

The two-service split is a decision, not an open packaging proposal. Wider repository
organization remains separate; any later consolidation must preserve these ownership
and API boundaries rather than merging console details into the core.

## 3. Evidence from the fork

Inspected account commit: `f7b1bc2509452b9e97dad592f255dbe85d674200`.

Inspected friends commit: `eb85ca351ddc469859974d45bf74989c7260e3d1`.

The current account process combines NNAS, NASC, public HTTP APIs, gRPC v1/v2,
connection checks, account-settings pages, asset serving, and optional datastore
support. Its documented runtime requires Node.js and MongoDB; integrations include
Redis, email, object storage, captcha, and services outside our initial scope.

The friends fork uses the Go account v2 protobuf package. Direct calls observed are
`GetUserData`, `GetNEXPassword`, and `ExchangeNEXTokenForUserData`; the latter sends
game-server ID `00003200`. Its common NEX dependencies may introduce additional
account calls, so these three methods are a starting set, not a complete contract.

Upstream distinguishes PNID identity, NEX accounts, owning PIDs, and devices. Do not
collapse these into a single integer or assume one console equals one device record.
Friends currently persists platform-specific friendships in its own PostgreSQL tables;
moving the canonical graph to account requires changes in that fork too.

Two inspected behaviors deserve explicit decisions in the rewrite:

- NEX token exchange contains a TODO for game-server and system/token-type checks.
  OpenPak must enforce audience and type validation, with negative compatibility tests.
- `GetUserData` exposes email, birthdate, device information, and broad permissions.
  Audit actual consumers and minimize responses; do not collect unnecessary fields
  merely because they exist in the upstream schema.

## 4. Users and required journeys

| User | Journey | Successful outcome |
| --- | --- | --- |
| Player | Register, verify email, sign in, recover access | One recoverable OpenPak account |
| Console owner | Link a console identity and sign in from a supported client | Stable identifiers and valid credentials without another master account |
| Player | Request, accept, block, or remove a friend | One consistent relationship across supported connection layers |
| Player | Inspect profile, achievements, sessions, and console links | Clear visibility and control over account data |
| Player | Report abuse, export data, or delete account | Trackable requests and effective access revocation |
| Moderator | Review reports, impose or lift a ban | Audited actions enforced across connected services |
| Operator | Deploy, back up, restore, rotate credentials | Documented operation of the Go stack |
| Service developer | Resolve identity and validate a service token | Versioned contracts with predictable failures |

## 5. Scope and compatibility policy

This PRD covers a coordinated account v1 across both services. The `account` core
includes identity lifecycle, web account management, generic adapter links, canonical
friends, third-party OAuth, moderation, privacy operations, and the achievement scope
resolved in section 13. `nn-account` supplies Wii U/3DS compatibility for the agreed
client matrix and the account protocol integration consumed by `nn-friends`.

The core's adapter contracts are console-neutral; Switch identifiers stay in a future
Switch adapter. Implementing Switch DAUTH, BAAS, NPLN, or game servers is separate work;
an account extension point alone does not constitute Switch support.

Compatibility means matching observable behavior required by supported clients:
host/path routing, methods, headers, encodings, field widths, token semantics, error
codes, and state transitions. Match exact bytes where required by the protocol or
client; otherwise compare meaning while accounting for timestamps and random values.
Do not preserve an upstream defect simply to make a snapshot test pass.

| Upstream surface | Rewrite disposition |
| --- | --- |
| NNAS `/v1/api/people`, devices, profiles, Miis | Port required lifecycle and profile operations; inventory every route |
| NNAS `/v1/api/oauth20/access_token/generate` | Port console authentication; separate it from third-party OAuth |
| NNAS `/v1/api/provider` NEX/service tokens | Port issuance and service discovery with explicit audience validation |
| NNAS support, mapped IDs, time, content metadata, account settings | Port client-required behavior in Go; use OpenPak-authored content |
| NASC `POST /ac`, `LOGIN` and `SVCLOC` | Port request encoding, account mapping, locator/token responses, and errors |
| Account gRPC v2 | Preserve contracts used by friends and selected dependencies through a compatibility adapter |
| Account gRPC v1 and upstream web/API gRPC | Inventory consumers; implement only if an in-scope consumer requires them |
| Upstream public web APIs | Replace with a documented OpenPak API; retain specific compatibility routes only when needed |
| Connection checks and CBVC | Determine client dependency; port necessary behavior and document unsupported routes |
| Local profile assets | Support required user-owned profile/Mii data with limits and privacy controls |
| General datastore, Miiverse, BOSS, commerce, Discord, forum integrations | Excluded from this rewrite's first release |

Every inventoried route/RPC must be classified as required, deferred, or excluded,
with a reason, consumer, owning service, and expected unsupported response. All NNAS,
NASC, Pretendo compatibility RPCs, and Nintendo-specific profile assets belong to
`nn-account`; the new generic web/API surface belongs to `account`. Deferred functions must
not return success while discarding their intended effect.

Other exclusions: Pretendo user-data import or federation, automatic migration of
Nintendo accounts, vendor account access, emulator forks, matchmaking implementation,
gameplay hosting, anti-cheat, and a guarantee of compatibility with every title.

## 6. Functional requirements

### FR-1: Identity and recovery

Owner: `account`. Nintendo routes delegate account operations through authenticated
internal APIs; `nn-account` never stores a second web password hash or recovery database.

- Create an immutable internal account ID independent of username, email, PID, and device ID.
- Support registration, email verification, login/logout, session listing/revocation,
  password reset, email change, and account status. Specify normalization and uniqueness
  rules before implementation; updates must remain unique under concurrent requests.
- Verification and recovery credentials are expiring, single-use, and rate-limited.
  Public responses must not unnecessarily reveal whether an email is registered.
- Website passwords are stored as adaptive password hashes. Protocol-specific NEX
  credentials are separate secrets; never return or derive a retrievable web password.
- Collect the agreed age-threshold attestation without requiring a full birthdate.
  Any console-required age fields need a documented compatibility and privacy decision.

### FR-2: Console identities and devices

- `account` owns generic links containing an account ID, adapter namespace, opaque
  adapter-subject ID, link ID, lifecycle state, and timestamps. The core does not parse
  or store Nintendo PIDs, NEX credentials, device serials, or Switch BAAS/NSA identifiers.
- `nn-account` owns its adapter subjects, PNID/NEX mappings, device bindings, protocol
  credentials, and Nintendo-specific profile data. A subject may have multiple device
  bindings where supported. Preserve separate PNID/NEX identifiers and ownership mapping.
- Enforce generic link uniqueness in the core and platform identifier uniqueness in the
  adapter. Preserve numeric widths; expose large values as strings in JSON where precision matters.
- Linking requires recent account authentication and proof appropriate to the client.
  Knowing a PID, serial, or username alone is not proof of ownership.
- Unlinking revokes the link's credentials and tokens without deleting the master account.
  Define relinking and transfer rules before exposing either action. IDs are not casually
  recycled after unlinking or deletion.
- A device-only 3DS flow may require provisional protocol records. Establish how these
  are claimed by a master account before enabling that flow; never silently create a
  second user identity or treat an unclaimed record as a verified account.

### FR-3: Console authentication and service discovery

Owner: `nn-account`, with identity verification and account/link policy supplied by `account`.

- Implement the selected NNAS and NASC flows and the registered services needed by friends.
- Bind each credential/token to its purpose, subject, platform, service/title audience,
  lifetime, and revocation state as applicable. Reject wrong-type, wrong-audience,
  expired, revoked, disabled-account, and malformed tokens on every validation path.
- Expiration checks are synchronous; correctness must not depend on background cleanup.
- Preserve the gRPC message fields, service names, metadata, and errors required by the
  pinned friends client at `nn-account`. Construct responses from adapter data and narrowly
  scoped core lookups. Restrict retrievable NEX credentials to authorized Nintendo services.
- Store the service registry in an operator-managed form: title/service identifiers,
  environment, endpoints, maintenance state, and references to secrets. No live keys
  or client captures enter the repository.
- Console credentials, web sessions, and third-party OAuth tokens are separate domains;
  none is accepted as another merely because it resolves to the same user.

### FR-4: One friend graph

- Account owns canonical relationships: pending, accepted, removed, and blocked, including
  request direction and actor. Operations must be authorized, transactional, and retry-safe.
- `nn-friends` keeps live presence and protocol-specific presentation state. Its graph
  reads/writes must use `account` operations once the shared-graph milestone lands.
  Resolve Nintendo PIDs through `nn-account`; submit graph operations using core account
  IDs and caller authorization. The core never accepts a PID as an account identifier.
- Model Wii U and 3DS workflow differences explicitly. Define how pending or one-sided
  console relationships map to the shared graph before porting handlers.
- Blocking prevents new requests and inappropriate presence/profile visibility. Define
  cross-platform projection and enforce it in adapters, including active sessions.
- During the first authentication integration, the unmodified friends database may own
  the graph temporarily. This is an internal milestone only; it cannot pass account v1.
  Do not introduce uncontrolled dual writes between account and friends databases.

### FR-5: Web account experience and third-party OAuth

Owner: `account`. Nintendo's embedded account-settings pages remain in `nn-account`
and delegate shared account changes to the core. Browser UI may request authorized
adapter details, but those details do not become fields in the core's identity schema.

- Serve registration, verification, sign-in/recovery, profile, console links, friends,
  sessions, application consent, reports, export, and deletion using Go handlers/templates.
- Provide keyboard-accessible forms, clear validation, and actionable service errors.
- Provide third-party authorization-code OAuth with PKCE, registered redirect URIs,
  explicit scopes/consent, revocation, and refresh-token rotation where refresh is supported.
  An OAuth client cannot choose arbitrary account or console privileges.
- Keep public API contracts independent of the Nintendo wire APIs. Specify OpenID Connect
  separately if third-party sign-in requires it; do not imply OAuth alone defines identity claims.

### FR-6: Profiles, achievements, trophies, and leaderboards

Owner: `account` for shared records and policy; title/platform adapters for protocol
translation and platform-specific interpretation.

- The account model supports platform/title-scoped achievement definitions and user awards,
  timestamps, progress where applicable, provenance, and profile visibility.
- Only an authorized title/platform integration can submit awards or scores; submissions
  require stable event IDs for deduplication. User-submitted claims cannot masquerade as
  verified awards. No anti-cheat in v1 does not mean unrestricted writes.
- Preserve platform-specific achievement/trophy semantics; do not invent a universal score
  or claim parity for unsupported platforms. Title services own game-specific score rules;
  account supplies identity, authorization, and the cross-console profile view.
- Define the first end-to-end award and leaderboard acceptance scenarios before calling
  this feature complete. Synthetic fixtures prove the foundation, not console synchronization.

### FR-7: Moderation and access enforcement

Owner: `account` for reports and account-level bans; each adapter enforces decisions
on its credentials and connections. Adapter-specific restrictions cannot override a core ban.

- Players can submit reports and view their submission status. Moderators can review,
  annotate, resolve, ban, and unban within explicitly assigned permissions.
- Every privileged action records actor, target, reason, timestamp, and result. Report
  evidence and staff notes are not public profile data.
- Account-level bans prevent new authentication and revoke applicable sessions/tokens.
  Connected services must consume invalidation or revalidate; proposed enforcement target
  is within 60 seconds for active sessions and immediately for new issuance/validation.
- Ban expiry, deletion, and recovery cannot accidentally restore previously revoked secrets.
  Moderator access requires stronger protection, including MFA before public release.

### FR-8: Export, deletion, and data minimization

Owner: `account` for orchestration and core data; `nn-account` and `nn-friends` for
their own records. Export and deletion are not complete until required consumers respond.

- Export includes the user's account, links, relationships, awards, sessions metadata,
  consent, and eligible report history. Exclude secrets and other users' confidential data.
- Deletion requires reauthentication, immediately disables access, and starts a retryable
  cleanup across account, friends, assets, and other registered consumers. Show its status.
- Specify retention and treatment for reports, audit records, identifiers, and backups.
  Do not promise instantaneous erasure from offline backups. Restores must reapply deletion
  records so removed users are not unintentionally reactivated.
- The data inventory must include all product data above, not just email/password/console IDs.
  Avoid full birthdates, gender, real names, phone numbers, and geolocation by default.
- Existing upstream fields receive a retain/omit/derive decision with client evidence.
  Privacy requirements here are product requirements, not a claim of legal compliance.

## 7. Service ownership and integration contracts

Build two independently executable Go services in `account` and `nn-account`. Each
owns its migrations, storage access, configuration, and tests. The core can run and
provide account functions with no Nintendo adapter deployed. Adapters call versioned
core APIs; they never access its tables or import its persistence implementation.

| Component | Owns | Does not own |
| --- | --- | --- |
| `account`: identity | Accounts, web credentials, sessions, consent, lifecycle, generic adapter links | PIDs, device records, console credentials, vendor protocols |
| `account`: social | Canonical friends, requests, blocks | Live console connections and Nintendo friendship encoding |
| `account`: profiles/awards | Profile visibility, namespaced award records, cross-platform presentation | Mii binary formats and game-specific scoring algorithms |
| `account`: moderation/privacy | Reports, bans, audit trail, export/delete orchestration | Direct writes to adapter databases |
| `nn-account` | NNAS/NASC, PNID/NEX mapping, devices, NEX credentials/tokens, service discovery, Nintendo profile assets, Pretendo-compatible gRPC | Web password storage, third-party OAuth provider, canonical graph, master identity |
| `nn-friends` | NEX connections, presence, console notifications | Canonical graph after integration |
| Future Switch/Xbox/PlayStation adapters | Their console identities, device details, credentials, and protocol mapping | Separate master identity or dependency on `nn-account` |

The core represents an adapter namespace and opaque subject as data, not as Nintendo
types or a console-specific database schema. A synthetic non-Nintendo adapter must be
able to link a subject and use account/social policy without changing core code or tables.
Shared Nintendo Go libraries may serve several adapters without coupling them through
the running `nn-account` service.

### Internal contracts

Define exact protobuf messages and error mappings in M0. Required capabilities are:

| Contract | Provider → consumer | Requirement |
| --- | --- | --- |
| Identity verification and minimal profile/status | `account` → authorized adapters | Narrowly scoped verification, no password/hash retrieval, consistent banned/deleted states |
| Generic link lifecycle | `account` ↔ adapter | Reserve/activate/unlink an opaque subject, prove ownership, reject conflicting claims |
| Nintendo identity resolution | `nn-account` → `nn-friends` | Map PID/protocol identity to active core link/account; no implicit ownership from a caller-supplied PID |
| Canonical graph operations | `account` → `nn-friends` and other consumers | Use core IDs, caller/actor authorization, retry-safe operations, and visibility policy |
| Account/link invalidation | `account` → all linked adapters/consumers | Versioned, durable events for bans, unlinking, deletion, and relevant credential/session changes |
| Export/delete participation | adapter → `account` orchestration | Scoped data export and acknowledged cleanup, with retries and visible failure state |
| Pretendo account gRPC v2 | `nn-account` → existing Nintendo clients | Preserve required wire contracts without exposing them as core APIs |

If a console flow submits a user's account password, the adapter may forward it only
to a dedicated, authenticated core verification operation over protected transport.
Do not persist or log it. Return a short-lived authorization result bound to the adapter
and operation; do not turn it into an unrestricted user session. Verify client-specific
credential transformations in M0 rather than assuming web login and console login match.

### Consistency and failure behavior

- A link becomes usable only after core and adapter ownership records agree. Use
  pending/active/revoking/revoked states, idempotency keys, and recovery/compensation for
  partial failures; do not assume a transaction spans both databases.
- Core unlink/ban/delete decisions immediately block new core authorization. Adapter
  issuance and validation must check authoritative account/link status; if unavailable,
  deny new authorization with a retryable protocol-appropriate error. Cached profiles
  must not silently become cached permission to issue credentials.
- Consumers process durable invalidation events with deduplication and version ordering.
  Active connections must revalidate within the FR-7 bound and terminate if they cannot
  establish current authorization. Test outages and delayed events, not just successful delivery.
- Core deletion marks access disabled first, then waits for adapter and friends cleanup
  acknowledgements. Partial completion remains visible and retries survive restarts.

### Runtime and code boundaries

Proposed runtime: two Go applications, PostgreSQL, core-owned SMTP configuration, and
optional object storage for bounded user-owned assets under the owning service's access
policy. One PostgreSQL installation may host both stores initially, but use separate
databases or schemas and database roles with no cross-service table access. Background
workers may run in each service binary. Use transactional delivery records for events
and cleanup; Redis is not an initial requirement.

Retain upstream protobuf Go dependencies at pinned versions in the Nintendo adapter
and clients. The core must not import Nintendo/Pretendo protocol packages. New OpenPak
APIs and generated clients are versioned separately. Select specific Go libraries/toolchain and
record their licenses in an implementation design, rather than embedding unverified
dependency choices in this PRD. No non-Go runtime/build chain is required to build,
test, or run either released service; isolated upstream comparison tooling may
remain available to developers while porting.

## 8. Security and operational requirements

- Fail startup on missing required secrets or invalid configuration. Privileged RPCs
  must not become unauthenticated when an API key is empty.
- Authenticate internal service callers with scoped credentials. Use protected transport
  or a documented encrypted proxy/tunnel compatible with the existing client; network
  location alone is not authorization. Rotate credentials without losing account data.
- Separate public, internal, and operator endpoints; apply body limits, timeouts, rate
  limits, secure session cookies, CSRF protection, and explicit trusted-proxy configuration.
- Isolate legacy console cryptography in adapters. Review parsers and token validation;
  use cryptographic randomness for generated credentials and recovery secrets.
- No passwords, tokens, private keys, full request bodies, or raw device credentials in
  logs. Keep retention short, configurable, and documented before public launch.
- Provide liveness/readiness, graceful shutdown, configuration examples, schema migrations,
  backup/restore instructions for each service, plus core-only and combined local
  deployments using synthetic accounts. `account` readiness must not depend on `nn-account`.
- New Go code must build and pass vet, meaningful automated tests, and race checks for
  concurrent account/graph operations. Test parsers with malformed and oversized inputs.
- Proposed initial load gate: on a documented 2-vCPU/4-GiB environment with 10,000 synthetic
  accounts, sustain 100 internal lookup/validation requests per second for 15 minutes,
  p95 under 150 ms, with no unexpected errors or identity mix-ups. Measure password
  operations separately. Confirm targets in the implementation design; do not claim results now.

## 9. Delivery milestones and exit criteria

| Milestone | Deliverable | Exit criteria |
| --- | --- | --- |
| M0: contract and port inventory | Route/RPC ownership catalog, extraction map, core/adapter contracts, compatibility matrix, license provenance | Every upstream surface assigned to core/adapter/deferred scope; transitive friends calls mapped; link and failure state machines specified; first client targets and prerequisites named |
| M1: console-agnostic Go core | `account` runtime/storage, register/verify/login/recovery, generic links, internal authorization API | Persistent lifecycle works with no Nintendo service deployed; synthetic non-Nintendo adapter links without core schema changes; no Nintendo imports or Node/Mongo account dependency |
| M2: Go Nintendo adapter | `nn-account` storage, PNID/NEX mapping, required v2 RPCs, NNAS/NASC routes, tokens, service discovery | Pinned friends clients resolve identity and authenticate through adapter/core; selected Wii U and 3DS paths connect; hardware/emulator results recorded separately; neither service reads the other's tables |
| M3: shared social and account controls | Core graph integration, bans/revocation, coordinated export/delete | Web and console friendship/block flows agree; bans reach active sessions; link/cleanup operations recover from partial failure; no competing graph stores |
| M4: account v1 | Both services, complete core web/OAuth experience, resolved achievement scope, operational hardening | All v1 requirements pass; independent builds/deployments and core-only operation verified; client coverage published; cross-service restore/deletion and OAuth checks pass; release questions resolved |
| M5: Switch integration | Separate Switch adapter consumes core contracts | Switch identity links to the same core account without using `nn-account`; core operations continue with Nintendo Network adapter stopped; separate client tests pass |

M1 and M2 are internal development checkpoints, not permission to drop account-first
product commitments. A launch requiring live achievements may depend on title work
outside this repository; account v1 cannot be declared complete on schema work alone.
No calendar dates are promised until M0 sizes the rewrite and client prerequisites.

## 10. Verification and rollout

Build a traceability matrix linking each requirement and required route/RPC to automated
coverage and, where necessary, a real-client check. Record client version, console family,
hardware/emulator, patches/configuration, flow, result, and evidence location. A passing
emulator test must not be labeled hardware support.

Required negative scenarios include duplicate/concurrent registration, account enumeration,
expired/reused recovery tokens, forged link claims, cross-account access, wrong token type
or audience, expired/revoked tokens, banned/deleted users, RPC authorization failures,
malformed XML/form payloads, concurrent friend requests, blocked presence, repeated award
events, dependency outages, interrupted deletion jobs, duplicate/out-of-order invalidation
events, partial link activation, and adapter impersonation. Verify service credentials
cannot access another adapter's subjects or the other service's tables.

Add architecture checks that `account` has no Nintendo protocol imports and runs without
`nn-account`. Exercise a synthetic non-Nintendo adapter against the same link, graph,
moderation, and deletion contracts. Measure the complete adapter-to-core request path,
not just isolated handler latency, when assessing the proposed performance target.

Use synthetic protocol fixtures and sanitized specifications. Where feasible, compare
against an isolated upstream server using fabricated accounts and locally supplied test
configuration. Do not copy production player data, keys, or raw captures into test assets.
If upstream comparison needs unavailable console secrets, record the untested dependency;
mock tests must not be presented as successful console integration.

OpenPak has no production account database to migrate at this stage. Start with a fresh
schema per service; importing Pretendo's users is out of scope. Create the `account`
repository during implementation and extract shared identity behavior into it, retaining
source provenance and applicable notices. Keep upstream source/history in `nn-account`
while porting its Nintendo behavior, then make Go the default build and remove obsolete application
runtime files when the corresponding behavior has been replaced. Preserve notices and
provenance. Use versioned migrations and a tested backup/restore process; document data
loss implications before any rollback to a snapshot.

Coordinate contract versioning and database migrations across services. Test restoration
of both stores and outstanding lifecycle events so a mismatched restore cannot reactivate
revoked links or lose deletion work. An adapter rollback must not require a core rollback
when the published contract remains compatible.

## 11. Licensing and provenance

Keep the fork's upstream license and applicable copyright notices. Record source paths
and revisions for derived Go ports. Independently authored OpenPak code follows the
project's AGPL-3.0-only policy, subject to compatibility with the reused components.

The inspected root LICENSE contains AGPL-3.0 text, while `package.json` declares ISC.
Resolve this metadata discrepancy before assigning derived-code license headers or
publishing a relicensing claim. Do not infer permission to remove notices from a language change.

## 12. Main risks and responses

| Risk | Response |
| --- | --- |
| Hidden protocol behavior and transitive gRPC dependencies | Complete the consumer inventory and preserve a pinned comparison baseline |
| Scope grows into a full network rewrite | Keep account APIs and identity ownership in scope; separate title/network milestones |
| 3DS device-only flows conflict with one-account policy | Design provisional identity claiming before exposing the flow |
| Canonical graph differs from console-specific friendship rules | Specify state mappings and validate both console families before cutover |
| Privacy conflicts with legacy fields | Minimize fields using consumer evidence; document unresolved compatibility requirements |
| Hardware tests require unavailable configuration or secrets | Track prerequisites and report unsupported/untested paths explicitly |
| Achievement parity delays account launch | Resolve required platforms and an actual end-to-end scenario before committing a release |
| Porting expands maintenance beyond available capacity | Reuse Go libraries and implement only the inventoried first-release surfaces |
| Service split introduces partial failures and stale permissions | Versioned contracts, durable invalidation, explicit link states, fail-closed authorization, and recovery tests |
| Nintendo assumptions leak into the core | Opaque adapter subjects, isolated schemas/imports, and a synthetic non-Nintendo integration gate |

## 13. Decisions still needed

These questions do not prevent M0 or a draft PRD; they gate the indicated downstream work.

1. Confirm PostgreSQL and the database/schema isolation layout for the two services before
   schema implementation. The `account` / `nn-account` split is already agreed.
2. Select the first Wii U and 3DS emulator/hardware configurations before M2 acceptance.
3. Define console-link cardinality, device-only identity claiming, and transfer policy before FR-2 ships.
4. Define which achievement/trophy platforms and leaderboard behavior constitute “full parity
   from the start,” and whether live title integration gates account v1, before M4 scope is fixed.
5. Set retention periods, deletion timing, age-field handling, and the interpretation of local
   operational metrics before public release.
6. Confirm third-party OAuth consumers and whether OpenID Connect is required before FR-5 implementation.

## 14. Local source references

- [Account entry point](src/server.ts), [setup](SETUP.md), [package metadata](package.json).
- [NNAS routes](src/services/nnas/index.ts), [NASC handler](src/services/nasc/routes/ac.ts).
- [gRPC registration](src/services/grpc/server.ts), [account v2 implementation](src/services/grpc/account/v2/implementation.ts).
- [NEX token exchange](src/services/grpc/account/v2/exchange-nex-token-for-user-data.ts),
  [user projection](src/services/grpc/account/v2/get-user-data.ts),
  [NEX credential lookup](src/services/grpc/account/v2/get-nex-password.ts).
- [PNID model](src/models/pnid.ts), [NEX account model](src/models/nex-account.ts), [device model](src/models/device.ts).
- Sibling `nn-friends` checkout: `go.mod`, `utility/authentication.go`,
  `globals/get_user_data.go`, `globals/account_details_by_pid.go`,
  `globals/account_details_by_username.go`, and `database/`.
- Workspace planning documents: `../research-third-party-networks.md` and
  `../openpak-bootstrap-prompt.md`. This PRD specializes their account scope; draft
  proposals here do not silently supersede confirmed product decisions.
