# MagiSend container image.
#
#   docker build --build-arg VERSION=v1.0.0 -t magisend .
#
# The same stages produce the static release binary:
#   docker build --target artifact --build-arg VERSION=v1.0.0 --output out .

FROM node:24.22.0-alpine3.24 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.27.2-alpine3.24 AS server
RUN apk add --no-cache build-base ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY internal ./internal
COPY web/backend-errors-contract.json web/
COPY --from=web /src/web/dist web/dist
ARG VERSION=dev
# Fully static (musl, SQLite compiled in): runs on any Linux distribution.
RUN CGO_ENABLED=1 go build -trimpath -tags embedui,netgo,osusergo,sqlite_omit_load_extension \
      -ldflags "-s -w -X main.version=${VERSION} -linkmode external -extldflags -static" \
      -o /out/magisend . \
 && /out/magisend --version \
 && mkdir -p /out/data /out/tmp && chown 10001:10001 /out/data && chmod 1777 /out/tmp

FROM scratch AS artifact
COPY --from=server /out/magisend /magisend

FROM scratch
COPY --from=server /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=server /out/magisend /magisend
COPY LICENSE THIRD-PARTY-NOTICES.md /
COPY --from=server --chown=10001:10001 /out/data /data
COPY --from=server /out/tmp /tmp
ENV MAGISEND_DATA=/data \
    MAGISEND_LISTEN=0.0.0.0:8080 \
    MAGISEND_INSTALL=docker
USER 10001:10001
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD ["/magisend", "--healthcheck"]
ENTRYPOINT ["/magisend"]
