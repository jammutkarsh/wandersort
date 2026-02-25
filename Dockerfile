FROM golang:1.26.0-alpine AS builder
WORKDIR /build
ENV CGO_ENABLED=0
ENV GOOS=linux

# Cache modules
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest and build
COPY . .
RUN go build -ldflags='-s -w' -o /server ./cmd

FROM alpine:3.18
RUN addgroup -S app && adduser -S -G app app
COPY --from=builder /server /server
RUN chmod +x /server
USER app
EXPOSE 8080
ENTRYPOINT ["/server"]
