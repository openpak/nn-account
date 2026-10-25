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
