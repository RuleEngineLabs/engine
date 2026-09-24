FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/engine ./cmd/engine && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/admin ./cmd/admin

FROM scratch
COPY --from=builder /out/engine /engine
COPY --from=builder /out/admin /admin
EXPOSE 8080
ENTRYPOINT ["/engine"]
