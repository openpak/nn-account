# Wii U/3DS Account Server

OpenPak's Wii U/3DS Account Server.

| | |
| --- | --- |
| Network | Wii U, 3DS |
| Role | Console account adapter: NNAS HTTP and the `account.v2` gRPC, over the OpenPak account core |
| Depends on | [`account`](https://github.com/openpak/account) (OpenPak account core), PostgreSQL |
| Used by | [`nn-friends`](https://github.com/openpak/nn-friends) |
| Image | `ghcr.io/openpak/nn-account` |
| Based on | [Pretendo `account`](https://github.com/PretendoNetwork/account) (AGPL-3.0) |
| Licence | AGPL-3.0-only |

## What it serves

| Endpoint | What |
| --- | --- |
| NNAS HTTP | Console sign-in, tokens, registration, people lookup, admin time, email validation |
| gRPC `account.v2` | `GetUserData`, `GetNEXPassword`, `ExchangeNEXTokenForUserData` for nn-friends |

## Run

```sh
cp example.env .env   # fill in required values
set -a; source .env; set +a
go run ./cmd/nn-account
```

Requires the account core running and PostgreSQL.

## Docs

- [Protocol](docs/protocol.md)
- [Evidence](docs/evidence.md)
- [Deploy](docs/deploy.md)
- [Notes](docs/notes.md)
- [Client testing](docs/client-testing.md)
- [Handles](docs/handles.md)
- [M0 inventory](docs/M0-INVENTORY.md)

## Licence

AGPL-3.0-only. See [LICENSE](LICENSE).
