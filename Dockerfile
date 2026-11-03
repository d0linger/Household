FROM golang:1.27.3-alpine3.24@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd ./cmd
COPY internal ./internal
RUN GOMAXPROCS=2 GOMEMLIMIT=2GiB CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/household ./cmd/household

FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6 AS runtime
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
