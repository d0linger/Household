FROM golang:1.27.4-alpine3.24@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd ./cmd
COPY internal ./internal
RUN GOMAXPROCS=2 GOMEMLIMIT=2GiB CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/household ./cmd/household

FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a AS runtime
RUN apk add --no-cache ca-certificates tzdata poppler-utils font-liberation && addgroup -S -g 10001 household && adduser -S -D -H -u 10001 -G household household && mkdir -p /data/uploads && chown -R 10001:10001 /data
COPY --from=build /out/household /usr/local/bin/household
USER 10001:10001
WORKDIR /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD ["household","healthcheck"]
ENTRYPOINT ["household"]
CMD ["serve"]

FROM runtime AS ocr
USER root
RUN apk add --no-cache tesseract-ocr tesseract-ocr-data-deu tesseract-ocr-data-eng
USER 10001:10001
