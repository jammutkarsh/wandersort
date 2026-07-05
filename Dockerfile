FROM golang:1.26-alpine AS base

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download -x

FROM base AS build
COPY . .
RUN go build ./...

FROM build AS test
RUN go vet ./...
RUN go test -count=1 ./...
