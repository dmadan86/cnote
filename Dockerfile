# C Note images (ADR-001). Targets: api (default, the Go API: `api`, `api migrate`), collab (live editing), web (Next.js).
#   docker build --target api -t cnote-api .      docker build --target collab -t cnote-collab .
#   docker build --target web -t cnote-web .      (API_URL is baked into the web build: --build-arg API_URL=https://api.example.com)

FROM golang:1.24-bookworm AS api-build
WORKDIR /src/apps/api
COPY apps/api/go.mod apps/api/go.sum ./
RUN go mod download
COPY apps/api ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api

FROM node:24-bookworm-slim AS node-deps
RUN corepack enable
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/collab/package.json apps/collab/
COPY apps/web/package.json apps/web/
COPY apps/mobile/package.json apps/mobile/
RUN pnpm install --frozen-lockfile --filter @cnote/collab --filter @cnote/web

FROM node-deps AS web-build
ARG API_URL=http://localhost:4000
ARG NEXT_PUBLIC_COLLAB_URL=ws://localhost:4000
ENV API_URL=$API_URL NEXT_PUBLIC_COLLAB_URL=$NEXT_PUBLIC_COLLAB_URL
COPY apps/web apps/web
# the one file web shares with the gateway (the editor schema number), imported by relative path
COPY apps/collab/src/doc-schema.ts apps/collab/src/doc-schema.ts
RUN pnpm --filter @cnote/web build

FROM node:24-bookworm-slim AS web
RUN corepack enable && useradd --system --uid 10001 cnote
WORKDIR /src
COPY --from=web-build --chown=cnote /src /src
USER cnote
ENV NODE_ENV=production PORT=3000
EXPOSE 3000
CMD ["pnpm", "--filter", "@cnote/web", "start"]

FROM node-deps AS collab
RUN useradd --system --uid 10001 cnote
COPY apps/collab apps/collab
USER cnote
ENV COLLAB_PORT=4300
EXPOSE 4300
CMD ["pnpm", "--filter", "@cnote/collab", "start"]

# Last stage: what a plain `docker build .` produces.
FROM debian:bookworm-slim AS api
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* \
  && useradd --system --uid 10001 cnote
COPY --from=api-build /out/api /usr/local/bin/api
COPY migrations /migrations
USER cnote
ENV PORT=4000 COOKIE_SECURE=true MIGRATIONS_DIR=/migrations
EXPOSE 4000
CMD ["api"]
