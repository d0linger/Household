FROM golang:1.27.4-alpine3.24@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd ./cmd
COPY internal ./internal
# Release identity for backups and the status page; local builds keep the defaults.
ARG HOUSEHOLD_VERSION=0.1.0
ARG HOUSEHOLD_COMMIT=unknown
RUN GOMAXPROCS=2 GOMEMLIMIT=2GiB CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X household/internal/buildinfo.Version=${HOUSEHOLD_VERSION} -X household/internal/buildinfo.Commit=${HOUSEHOLD_COMMIT}" -o /out/household ./cmd/household

FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf AS runtime
# restic and the PostgreSQL 18 client let the same image run scheduled backups.
RUN apk add --no-cache ca-certificates tzdata poppler-utils font-liberation restic postgresql18-client && addgroup -S -g 10001 household && adduser -S -D -H -u 10001 -G household household && mkdir -p /data/uploads /data/backups /data/backup-exchange /work && chown -R 10001:10001 /data /work
COPY --from=build /out/household /usr/local/bin/household
ENV HOUSEHOLD_SECRETS_DIR=/run/secrets HOUSEHOLD_UPLOAD_DIR=/data/uploads RESTIC_REPOSITORY=/data/backups
USER 10001:10001
WORKDIR /data
EXPOSE 8080
# The first start sets up and migrates the database, which can take minutes.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10m --start-interval=5s --retries=3 CMD ["household","healthcheck"]
ENTRYPOINT ["household"]
CMD ["serve"]

FROM runtime AS ocr
USER root
RUN apk add --no-cache tesseract-ocr tesseract-ocr-data-deu tesseract-ocr-data-eng
USER 10001:10001
