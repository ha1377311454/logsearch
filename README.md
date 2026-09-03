# LogSearch

LogSearch 从 Chrome DevTools 中提取前端请求的 Request ID，并并发查询 Kubernetes 各节点的容器日志。

完整设计见 [docs/design.md](docs/design.md)，部署说明见 [docs/deployment.md](docs/deployment.md)，插件使用说明见 [docs/extension.md](docs/extension.md)。

## 组件

- `logsearch-agent`：Connect/gRPC/gRPC-Web 日志查询服务，以 DaemonSet 运行。
- `logsearch-cli`：命令行查询客户端。
- `extension`：Chrome Manifest V3 DevTools 扩展。

## 本地运行

复制并调整 `configs/agent.yaml` 中的日志根目录，然后运行：

```bash
go run ./cmd/logsearch-agent -config configs/agent.yaml
```

命令行查询：

```bash
go run ./cmd/logsearch-cli \
  -server http://127.0.0.1:9000 \
  -keywords request-id-123 \
  -mode all
```

多个关键词用逗号分隔。`all` 表示所有关键词必须出现在同一行，`any` 表示命中任意关键词。

## 生成协议代码

安装 `protoc-gen-go` 和 `protoc-gen-connect-go` 后执行：

```bash
make generate
```

生成文件已提交，普通构建不需要重新生成。
