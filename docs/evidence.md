# Evidence: nn-account

## Tests

```sh
docker run -d --name openpak-pg-test -e POSTGRES_PASSWORD=test \
  -e POSTGRES_DB=account_test -p 54329:5432 postgres:18-alpine
go test -tags integration -race -count=1 ./...
```

The integration suite builds the real core binary and exercises the full
console journey: register → sign in → NEX token → identity exchange, plus
fail-closed, audience-mismatch, and ban negative scenarios.
