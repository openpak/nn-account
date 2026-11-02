# Deploy: nn-account

## Run

```sh
cp example.env .env   # fill in required values
set -a; source .env; set +a
go run ./cmd/nn-account
```

Requires the account core running and PostgreSQL.

## Image

The Dockerfile builds `nn-account` and `nn-handle-migrate` and exposes ports 7001 and 8001.
`.github/workflows/release.yml` runs on `v*.*.*` tags and publishes `ghcr.io/openpak/nn-account`
with the version tag and `:latest` for amd64 and arm64.

## Tests

```sh
docker run -d --name openpak-pg-test -e POSTGRES_PASSWORD=test \
  -e POSTGRES_DB=account_test -p 54329:5432 postgres:18-alpine
go test -tags integration -race -count=1 ./...
```

The integration suite builds the real core binary and exercises the full console journey
(register, sign in, NEX token, identity exchange) plus fail-closed, audience-mismatch and ban
negative scenarios.
