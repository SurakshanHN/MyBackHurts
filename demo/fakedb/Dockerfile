# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /workspace

# Copy dependency manifests
COPY go.mod go.sum ./
RUN go mod download

# Copy source code and build
COPY demo/fakedb/ ./demo/fakedb/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/fakedb ./demo/fakedb

# Runtime stage
FROM alpine:3.21

RUN apk --no-cache add ca-certificates

WORKDIR /
COPY --from=builder /bin/fakedb /bin/fakedb

EXPOSE 8080

ENTRYPOINT ["/bin/fakedb"]
