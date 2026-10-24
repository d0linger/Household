FROM golang:1.27.3-alpine3.24@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/household ./cmd/household
FROM postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
RUN apk add --no-cache restic tzdata && addgroup -S -g 10001 household && adduser -S -D -H -u 10001 -G household household && mkdir /work /repository && chown 10001:10001 /work /repository
COPY --from=build /out/household /usr/local/bin/household
USER 10001:10001
WORKDIR /work
ENTRYPOINT ["household"]
CMD ["backup-loop"]
