# syntax=docker/dockerfile:1
# The Go adapter (cmd/nn-account). The legacy TypeScript runtime under src/ is not built here.

FROM golang:1.25-alpine AS build
WORKDIR /src
RUN --mount=type=cache,target=/go/pkg/mod/ \
	--mount=type=bind,source=go.sum,target=go.sum \
	--mount=type=bind,source=go.mod,target=go.mod \
	go mod download -x
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod/ \
	CGO_ENABLED=0 go build -trimpath -o /out/nn-account ./cmd/nn-account

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app && apk add --no-cache ca-certificates
USER app
COPY --from=build /out/nn-account /usr/local/bin/nn-account
EXPOSE 7001 8001
ENTRYPOINT ["/usr/local/bin/nn-account"]
