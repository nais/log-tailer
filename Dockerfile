FROM cgr.dev/chainguard/go:latest AS builder

WORKDIR /app
COPY . /app
RUN CGO_ENABLED=0 go build -o log-tailer main.go

FROM cgr.dev/chainguard/static:latest

WORKDIR /app
COPY --from=builder /app/log-tailer /app/log-tailer

ENTRYPOINT ["/app/log-tailer"]
