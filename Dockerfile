# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (c) 2026 Utkarsh Chourasia

# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS base

WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

FROM base AS deps
RUN apk add --no-cache curl

ARG EXIFTOOL_VERSION
RUN mkdir -p /deps \
    && curl -fsSL "https://wandersort.utkarshchourasia.in/files/exiftool-${EXIFTOOL_VERSION}-linux.tar.gz" \
      | tar xz -C /deps \
    && curl -fsSL "https://locationdb.utkarshchourasia.in/location.db" \
      -o /deps/location.db

FROM base AS build
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags='-s -w' -o /out/wandersort .

FROM build AS test
COPY --from=deps /deps /deps
ENV HOME=/root
RUN mkdir -p $HOME/.wandersort/bin \
    && cp /deps/exiftool $HOME/.wandersort/bin/exiftool \
    && chmod +x $HOME/.wandersort/bin/exiftool \
    && cp /deps/location.db $HOME/.wandersort/location.db
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go vet ./... && go test -count=1 -cover ./...

FROM scratch AS release
COPY --from=build /out/wandersort /wandersort
