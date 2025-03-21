# 构建阶段
FROM golang:1.24.1-alpine AS builder

# 设置工作目录
WORKDIR /app

# 安装必要的构建工具
RUN apk add --no-cache git ca-certificates tzdata

# 复制go.mod和go.sum文件
COPY go.mod go.sum* ./

# 下载依赖
RUN go mod download

# 复制源代码
COPY . .

# 编译应用程序
# 使用CGO_ENABLED=0生成静态二进制文件
# 使用-ldflags="-s -w"减小二进制体积
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/honeypot

# 运行阶段
FROM alpine:3.18

# 添加非root用户
RUN adduser -D -u 1000 appuser

# 安装运行时依赖
RUN apk add --no-cache ca-certificates tzdata

# 从builder阶段复制编译好的应用
COPY --from=builder /app/honeypot /usr/local/bin/honeypot

# 复制配置文件
COPY --from=builder /app/config.json /etc/honeypot/config.json

# 设置工作目录
WORKDIR /home/appuser

# 切换到非root用户
USER appuser

# 设置环境变量
ENV CONFIG_PATH=/etc/honeypot/config.json

# 暴露所需端口
EXPOSE 8080

# 运行应用
CMD ["honeypot"] 