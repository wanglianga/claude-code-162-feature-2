# 区域再生资源回收服务 - 多阶段构建
FROM golang:1.19-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/recycle-app ./cmd/app

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 appuser
WORKDIR /app
COPY --from=build /out/recycle-app /app/recycle-app
COPY migrations /app/migrations
USER appuser
EXPOSE 8080
# 启动即自动执行 migrations; seed 与运行模式由 compose/命令行控制:
#   recycle-app migrate | seed | server | worker | all
ENTRYPOINT ["/app/recycle-app"]
CMD ["all"]
