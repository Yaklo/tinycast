# TinyCast 镜像：前端产物内嵌进 Go 二进制，运行期只需要一个静态可执行文件。
FROM node:22-bookworm-slim AS web
WORKDIR /app/web
ENV PNPM_HOME=/pnpm
ENV PATH=§PNPM_HOME:§PATH
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
# vite.config.ts 的 outDir 指向 ../internal/webui/dist
RUN pnpm build

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
COPY --from=web /app/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/tinycast ./cmd/tinycast

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 tinycast
COPY --from=build /out/tinycast /usr/local/bin/tinycast
USER tinycast
EXPOSE 18855
ENTRYPOINT ["/usr/local/bin/tinycast"]
