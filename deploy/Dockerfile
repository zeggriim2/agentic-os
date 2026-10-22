# syntax=docker/dockerfile:1
# Build multi-étapes : UI (node) → binaire Go statique → image d'exécution minimale (UID 1000).

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.23-alpine AS build
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

FROM alpine:3.20
RUN apk add --no-cache git ca-certificates \
 && addgroup -g 1000 aos \
 && adduser -D -H -u 1000 -G aos -s /sbin/nologin aos
COPY --from=build /out/agentic-os /usr/local/bin/agentic-os
USER 1000:1000
ENV AOS_GIT_BINARY=/usr/bin/git
ENTRYPOINT ["/usr/local/bin/agentic-os"]
CMD ["serve"]
