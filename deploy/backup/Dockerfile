FROM golang:1.27.4-alpine3.24@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd ./cmd
COPY internal ./internal
RUN GOMAXPROCS=2 GOMEMLIMIT=2GiB CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/household ./cmd/household
FROM postgres:18-alpine@sha256:b6972e1115a4ff8eba5d7f7d19184926d1e16d7d1136ae1e1a7ebf286da0f015
RUN apk add --no-cache restic tzdata && addgroup -S -g 10001 household && adduser -S -D -H -u 10001 -G household household && mkdir /work /repository && chown 10001:10001 /work /repository
COPY --from=build /out/household /usr/local/bin/household
USER 10001:10001
WORKDIR /work
ENTRYPOINT ["household"]
CMD ["backup-loop"]
