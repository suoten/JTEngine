# 注意：不要加 `# syntax=docker/dockerfile:1.x` 指令——它会让冷构建额外拉取
# docker/dockerfile 镜像（国内网络的又一个依赖点）。Docker 23+/BuildKit 内置
# 前端已支持本文件用到的 --mount=type=cache、COPY --chown、多阶段构建。
# AUTO-FIX-2026-07-21 [P1-可靠性]: Dockerfile 优化
# - 基础镜像 alpine:3.19 → alpine:3.20（最新稳定版，安全补丁更及时）
# - 添加 --mount=type=cache BuildKit 缓存优化（Go module + npm + go-build）
# - 最终镜像不包含编译工具链（gcc/musl-dev 仅在 builder 阶段）
# - 使用 COPY --chown 替代单独的 RUN chown 步骤（减少一层）
# - Go 版本对齐 go.mod（跟随 go.mod 的 go 指令升级）

# =============================================================================
# Stage 1: 前端构建
# =============================================================================
FROM node:20-alpine AS frontend-builder

WORKDIR /build/web
COPY web/package.json web/package-lock.json* ./

# [P1-可靠性] npm registry 可配置化（默认官方源，国内可通过 build-arg 覆盖）
ARG NPM_REGISTRY=https://registry.npmjs.org

# [P1-可靠性] BuildKit 缓存：npm 缓存目录持久化，加速重复构建
RUN --mount=type=cache,target=/root/.npm \
    npm ci --registry=${NPM_REGISTRY}

COPY web/ .
RUN npm run build

# =============================================================================
# Stage 2: 后端构建
# =============================================================================
# Go 版本对齐 go.mod（go 1.25.0；用旧版镜像会报 "go.mod requires go >= 1.25.0"）
FROM golang:1.25-alpine AS backend-builder

# GOPROXY 可通过 build-arg 覆盖；默认 goproxy.cn（国内构建必须，proxy.golang.org 不可达）
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

# 安装 C 编译器（go-sqlite3 依赖 CGO），仅在此阶段存在
RUN apk --no-cache add gcc musl-dev

WORKDIR /build

# [P1-可靠性] 先复制依赖文件，利用 Docker 层缓存
COPY go.mod go.sum ./

# 注意：不要在此处单独执行 `go mod download`——它会下载 go.mod 里全部 require，
# 包括三个未发布的付费模块（module-protocol-809/905/1045@v0.0.0，仅 -tags builtin
# 构建才需要，由工作区 go.work 提供）。默认构建不 import 它们，由下方 go build
# 按需拉取实际依赖即可。

COPY . .
COPY --from=frontend-builder /build/web/dist ./web/dist

# 版本号注入 main.Version（运行日志/healthz 会显示，便于远程排障确认构建版本）
ARG VERSION=dev

# CGO_ENABLED=1：mattn/go-sqlite3 需要 CGO 编译，禁用会导致构建失败
# -ldflags="-s -w"：去除调试信息，减小二进制体积
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w -X main.Version=${VERSION}" -o jte ./cmd/jte/

# =============================================================================
# Stage 3: 运行时镜像（不含编译工具链）
# =============================================================================
FROM alpine:3.20

# [P1-可靠性] OCI 标准 LABEL 元数据
LABEL org.opencontainers.image.title="JTE Engine" \
      org.opencontainers.image.description="JT/T 808/809/905/1078 + GB28181 + GB/T 32960 车联网平台" \
      org.opencontainers.image.source="https://github.com/jte-engine/jte" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="JTE Engine"

# 安装运行时依赖（不含 gcc/musl-dev 等编译工具链）
# ca-certificates: TLS 证书；tzdata: 时区；wget: 健康检查
# /app/data、/app/spool、/app/modules 预创建并赋权：容器以非 root 用户 jte 运行，
# /app 属主为 root，运行时创建会 permission denied（sqlite DSN ./data/jte.db、spool
# 离线暂存、modules 供 license 本地密钥与审计日志写入）
RUN apk --no-cache add ca-certificates tzdata wget \
    && addgroup -S jte && adduser -S jte -G jte \
    && mkdir -p /app/data /app/spool /app/modules \
    && chown jte:jte /app/data /app/spool /app/modules

WORKDIR /app

# [P1-可靠性] 使用 COPY --chown 直接设置文件权限，减少一层 RUN
COPY --chown=jte:jte --from=backend-builder /build/jte ./
COPY --chown=jte:jte --from=backend-builder /build/configs ./configs

USER jte

EXPOSE 7611 8080

# [P1-可靠性] 健康检查（非 root 用户运行）
# 根据 JTE_API_TLS_ENABLED 选择协议
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD if [ "$JTE_API_TLS_ENABLED" = "true" ]; then \
        wget -qO- --no-check-certificate https://localhost:8080/healthz; \
      else \
        wget -qO- http://localhost:8080/healthz; \
      fi || exit 1

CMD ["./jte"]
