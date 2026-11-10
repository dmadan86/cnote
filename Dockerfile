# C Note backend: one image, entrypoints `serve`, `worker`, `migrate` (ADR-001).
FROM rust:1-slim-bookworm AS build
WORKDIR /src
COPY Cargo.toml Cargo.lock ./
COPY crates crates
COPY migrations migrations
RUN cargo build --release -p cnote-server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* \
  && useradd --system --uid 10001 cnote
COPY --from=build /src/target/release/cnote /usr/local/bin/cnote
USER cnote
ENV PORT=4000 RUST_LOG=info,sqlx=warn COOKIE_SECURE=true
EXPOSE 4000
CMD ["cnote", "serve"]
