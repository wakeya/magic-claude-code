# 阶段1: 构建前端
FROM node:24-alpine AS frontend-builder

WORKDIR /frontend

# 安装依赖
COPY internal/frontend/package.json internal/frontend/package-lock.json ./
RUN npm ci

# 复制前端源码并构建
COPY internal/frontend/ ./
RUN npm run build

# 阶段2: 构建 Go 二进制
FROM golang:1.26-alpine AS builder

WORKDIR /app

# 复制依赖文件
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 用构建好的前端替换 dist 目录
COPY --from=frontend-builder /frontend/dist ./internal/frontend/dist

# 构建
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o mcc ./cmd/server

# 阶段3: 运行
FROM alpine:latest

# 安装 CA 证书和时区数据
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# 复制二进制文件
COPY --from=builder /app/mcc .

# 创建数据目录
RUN mkdir -p /app/data

# 暴露端口
EXPOSE 443 8442

# 设置环境变量
ENV ADMIN_PASSWORD=admin123

# 启动
ENTRYPOINT ["./mcc"]
CMD ["-data", "/app/data"]
