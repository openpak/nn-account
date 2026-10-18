# Permanent account handles

Wii U/3DS console registration sends the typed NNID as the required core handle.
Core rejects names outside 6–15 ASCII letters/digits, letter first, and its shared
reserved/profanity policy. Console XML error 0002 rejects invalid/reserved/profane IDs, and 0100 rejects taken IDs;
no pending account bypass or adapter-local name validation list exists. The NNID
availability check also delegates to core and fails closed on outages.

Shadow creation uses GetAccount.handle verbatim, with no fallback or suffix if
that field is populated. Profile display fields read the current core account name (truncated
to the console's 10-character field), including existing PNIDs after a website
account-name edit. Console signup supplies a separate handle and leaves display
name empty so core defaults it to the handle. The settings applet directs account
name changes to the website; its Mii binary-data editor remains independent.
Binary Mii data and game-owned nicknames are never rewritten. Numeric PID and
3DS friend code are separate.
Legacy missing-handle accounts retain their old seeds during migration.

The alpha migration must update the existing pnids.username row, retaining PID,
NEX identity, credentials and core links. Owners review the old and chosen names
first; cached console NNIDs can require signing in again. There is no user-facing
rename path. On account_deleted, tokens are revoked and the old local username
is retired, freeing it locally; core still holds the handle for 90 days after
permanent deletion. A new owner receives a different PID and friend code.
