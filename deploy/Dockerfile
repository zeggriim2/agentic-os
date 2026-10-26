# syntax=docker/dockerfile:1
# Build multi-étapes : UI (node) → binaire Go statique → image d'exécution minimale (UID 1000).

# Images épinglées par digest (mises à jour par Dependabot, voir .github/dependabot.yml).
FROM node:26-alpine3.24@sha256:40cd011b55415bc894e45b02fdd5a5dfde056921b7d23a64c74f80497af48797 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-fund --ignore-scripts
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine3.24@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY web/embed.go ./web/embed.go
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
ARG REVISION=unknown
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" \
      -o /out/agentic-os ./cmd/agentic-os

FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
RUN apk upgrade --no-cache \
 && apk add --no-cache git ca-certificates \
 && addgroup -g 1000 aos \
 && adduser -D -H -u 1000 -G aos -s /sbin/nologin aos
COPY --from=build /out/agentic-os /usr/local/bin/agentic-os
USER 1000:1000
ENV AOS_GIT_BINARY=/usr/bin/git
ENTRYPOINT ["/usr/local/bin/agentic-os"]
CMD ["serve"]
