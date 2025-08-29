FROM rust:1 AS builder

RUN apt update -y; apt-get install gcc-x86-64-linux-gnu -y
ENV CARGO_BUILD_TARGET=x86_64-unknown-linux-musl
ENV RUSTFLAGS='-C linker=x86_64-linux-gnu-gcc'

ADD https://github.com/krallin/tini/releases/latest/download/tini-static-amd64 /tini
RUN chmod +x /tini

RUN rustup target add x86_64-unknown-linux-musl
RUN git clone https://github.com/uutils/coreutils

WORKDIR coreutils
RUN cargo build -p uu_tail --release --no-default-features

FROM scratch

WORKDIR /app

COPY --from=builder /tini /tini
COPY --from=builder coreutils/target/x86_64-unknown-linux-musl/release/tail /app/tail

ENTRYPOINT ["/tini", "--", "/app/tail", "-F"]
