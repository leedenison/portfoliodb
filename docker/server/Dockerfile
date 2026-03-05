FROM golang:1.25-alpine AS builder
WORKDIR /app
RUN go install github.com/bufbuild/buf/cmd/buf@latest
COPY go.mod go.sum ./
RUN go mod download
COPY proto/ proto/
COPY buf.yaml buf.gen.go.yaml ./
RUN buf generate --template buf.gen.go.yaml
COPY server/ server/
RUN go build -o /portfoliodb ./server/cmd/portfoliodb

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /portfoliodb /portfoliodb
COPY server/migrations /migrations
EXPOSE 50051
ENTRYPOINT ["/portfoliodb"]
CMD ["-grpc-addr", ":50051", "-db-url", "postgres://portfoliodb:portfoliodb@postgres:5432/portfoliodb?sslmode=disable"]
