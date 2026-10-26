FROM golang:1.27-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /prescaler ./cmd/prescaler

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /prescaler /prescaler
USER nobody
ENTRYPOINT ["/prescaler"]
