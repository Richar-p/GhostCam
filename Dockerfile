# Toolchain image: the host only needs Docker.
FROM golang:1.25-bookworm AS base
WORKDIR /src
ENV CGO_ENABLED=0

# Release builds: also collect third-party license texts (MIT/BSD/ISC/Apache
# require shipping them with binaries).
FROM base AS release
RUN go install github.com/google/go-licenses@v1.6.0

# Dev: run from mounted sources, with cloudflared available in the container.
FROM base AS dev
ARG TARGETARCH
RUN curl -fsSL -o /usr/local/bin/cloudflared \
      "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${TARGETARCH:-amd64}" \
 && chmod +x /usr/local/bin/cloudflared
CMD ["go", "run", "./cmd/ghostcam", "-headless", "-cloudflared", "/usr/local/bin/cloudflared", "-no-upnp", "-admin", "0.0.0.0:8081", "-out", "/src/recordings", "-web-dir", "/src/web"]

# End-to-end test runner: Chromium with a fake camera plays the phone.
FROM mcr.microsoft.com/playwright:v1.50.0-noble AS e2e
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg && rm -rf /var/lib/apt/lists/*
WORKDIR /e2e
RUN npm init -y >/dev/null && npm install playwright@1.50.0
ENV NODE_PATH=/e2e/node_modules

# Icon pipeline: web/icon.svg -> PNGs -> Windows resource (.syso, picked up by go build).
FROM base AS icons
RUN apt-get update && apt-get install -y --no-install-recommends librsvg2-bin && rm -rf /var/lib/apt/lists/* \
 && go install github.com/tc-hib/go-winres@v0.3.3
