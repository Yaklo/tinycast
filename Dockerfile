# TinyCast 镜像：前端产物内嵌进 Go 二进制，运行期只需要一个静态可执行文件。
FROM node:22-bookworm-slim AS web
WORKDIR /app/web
ENV PNPM_HOME=/pnpm
ENV PATH=${PNPM_HOME}:${PATH}
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
# vite.config.ts 的 outDir 指向 ../internal/webui/dist
RUN pnpm build

FROM golang:1.24-alpine AS build
WORKDIR /src
# 构建上下文里没有 .git，版本信息只能由外部传入；
# docker compose 会自动取 TINYCAST_VERSION / TINYCAST_COMMIT / TINYCAST_BUILD_TIME。
ARG VERSION=devel
ARG COMMIT=
ARG BUILD_TIME=
COPY go.mod ./
RUN go mod download
COPY . .
COPY --from=web /app/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w \
      -X tinycast/internal/version.Version=${VERSION} \
      -X tinycast/internal/version.Commit=${COMMIT} \
      -X tinycast/internal/version.BuildTime=${BUILD_TIME}" \
    -o /out/tinycast ./cmd/tinycast

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 tinycast \
    && mkdir -p /var/log/tinycast \
    && chown tinycast:tinycast /var/log/tinycast
LABEL org.opencontainers.image.title="TinyCast" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.source="https://github.com/Yaklo/tinycast"
# MIT 与 Apache-2.0 都要求分发的副本里带上版权与许可声明
COPY LICENSE THIRD-PARTY-NOTICES.md /usr/share/doc/tinycast/
COPY --from=build /out/tinycast /usr/local/bin/tinycast
USER tinycast
EXPOSE 18855
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:18855/api/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/tinycast"]
