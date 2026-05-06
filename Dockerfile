# 第一阶段：编译阶段
FROM golang:1.25-alpine AS builder

# 设置工作目录
WORKDIR /app

# 复制依赖文件并下载
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 编译应用 (CGO_ENABLED=0 确保静态链接)
RUN CGO_ENABLED=0 GOOS=linux go build -o mixfile-go ./main.go

# 第二阶段：运行阶段
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# 从编译阶段复制二进制文件
COPY --from=builder /app/mixfile-go .

# 暴露端口 (根据你的 server.go 实际端口修改)
EXPOSE 8080

# 运行应用
CMD ["./mixfile-go"]
