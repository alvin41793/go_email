FROM golang:1.18-alpine AS builder

WORKDIR /app

# 复制go.mod和go.sum
COPY go.mod ./
COPY go.sum ./

# 下载依赖
RUN go mod download

# 复制源代码
COPY . .

# 构建应用
RUN CGO_ENABLED=0 GOOS=linux go build -o go-email-service

# 使用轻量级的alpine镜像
FROM alpine:latest

WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /app/go-email-service .
COPY --from=builder /app/config ./config

# 暴露端口
EXPOSE 8080

# 启动应用
CMD ["./go-email-service"] 