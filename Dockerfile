# 构建阶段：使用 Go 工具链编译静态二进制。
FROM golang:1.25-alpine AS builder

WORKDIR /app

# 先拷贝依赖清单，提升 Docker 层缓存命中率。
COPY go.mod go.sum ./
RUN go mod download

# 再拷贝源码并编译服务。
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# 运行阶段：使用更小的 alpine 基础镜像。
FROM alpine:3.22

# 安装证书和时区数据，并创建非 root 用户。
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 appuser

WORKDIR /app
COPY --from=builder /out/server /app/server

EXPOSE 9901
# 以低权限用户启动应用。
USER appuser

CMD ["/app/server"]
