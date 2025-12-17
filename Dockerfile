FROM golang:1.25 AS builder

RUN apt update -y && apt install -y gcc-x86-64-linux-gnu
WORKDIR /app
COPY . /app
RUN go build -o log-tailer main.go

FROM scratch

WORKDIR /app
COPY --from=builder /app/log-tailer /app/log-tailer

ENTRYPOINT ["/app/log-tailer"]
