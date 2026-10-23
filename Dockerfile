# syntax=docker/dockerfile:1
#
# One image per server. Build any of the ten with:
#   docker build --build-arg SERVER=jwt-tools -t mcp-jwt-tools .
# and run it on stdio (no port is opened):
#   docker run --rm -i mcp-jwt-tools
#
# Base images are pinned by digest (multi-arch indexes).
ARG GO_IMAGE=golang:1.23.12-alpine@sha256:383395b794dffa5b53012a212365d40c8e37109a626ca30d6151c8348d380b5f
ARG RUNTIME_IMAGE=alpine:3.22@sha256:d406446bc05b82480756ce8f1287964d28768c23955fa9480d2a74a2eae7a654

# Cross-compile on the runner's own platform: the servers are pure Go with cgo off.
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG SERVER
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ internal/
COPY packages/ packages/
RUN if [ -z "${SERVER}" ] || [ ! -d "packages/${SERVER}/cmd/mcp-${SERVER}" ]; then \
      echo "Set --build-arg SERVER to one of: $(ls packages | tr '\n' ' ')" >&2; exit 1; \
    fi
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/mcp-server "./packages/${SERVER}/cmd/mcp-${SERVER}" \
 && cp "packages/${SERVER}/README.md" /out/README.md

FROM ${RUNTIME_IMAGE}
ARG SERVER
ARG VERSION=0.0.0-dev
LABEL org.opencontainers.image.title="mcp-${SERVER}" \
      org.opencontainers.image.description="MCP server ${SERVER} from dev-mcp-servers, stdio transport" \
      org.opencontainers.image.source="https://github.com/basitalisandhu/dev-mcp-servers" \
      org.opencontainers.image.url="https://github.com/basitalisandhu/dev-mcp-servers/tree/main/packages/${SERVER}" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      io.modelcontextprotocol.server.name="io.github.basitalisandhu/${SERVER}"
# git-insights shells out to git. Mount the repository at /repo; only that tree is marked safe for the
# container's non-root user, whose uid usually differs from the owner of the mounted files.
RUN if [ "${SERVER}" = "git-insights" ]; then \
      apk add --no-cache git \
      && git config --system --add safe.directory /repo \
      && git config --system --add safe.directory '/repo/*'; \
    fi \
 && adduser -D -u 1000 mcp
COPY --from=build /out/ /app/
WORKDIR /app
USER mcp
ENTRYPOINT ["/app/mcp-server"]
