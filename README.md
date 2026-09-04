# LogSearch

LogSearch 从 Chrome DevTools 中提取前端请求的 Request ID，并并发查询 Kubernetes 各节点的容器日志。

完整设计见 [docs/design.md](docs/design.md)，部署说明见 [docs/deployment.md](docs/deployment.md)，插件使用说明见 [docs/extension.md](docs/extension.md)。

## 组件

- `logsearch-agent`：Connect/gRPC/gRPC-Web 日志查询服务，以 DaemonSet 运行。
- `logsearch-cli`：命令行查询客户端。
- `extension`：Chrome Manifest V3 DevTools 扩展。

Agent 同时支持 kubelet `/var/log/pods` 标准输出日志，以及通过宿主机进程 `/proc/<pid>/root` 读取容器内部文件日志和轮转日志。进程日志规则可通过 `multiline.start_pattern` 将 Java 异常堆栈等多行内容合并为一条日志后再执行关键词匹配。

Kubernetes 清单：

- `deploy/logsearch-dev.yaml`：开发环境，NodePort，不鉴权。
- `deploy/logsearch-auth-ingress.yaml`：Bearer Token 鉴权，ClusterIP 和 Ingress。

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

`search.max_response_bytes` 控制单次响应总大小，`search.max_line_bytes` 控制单行返回大小；二者设置为 `-1` 时不限制。开发环境清单默认不限制，生产环境建议保留合理上限。

多行合并示例：

```yaml
process_logs:
  - name: example-api
    # 省略进程和文件匹配配置
    multiline:
      start_pattern: '^[0-9]{2}:[0-9]{2}:[0-9]{2}[.,][0-9]{3}'
max_multiline_bytes: 4194304
max_multiline_lines: 1000
```

匹配 `start_pattern` 的行开始一条新日志，其他行合并到上一条。关键词匹配、前文和后文均基于合并后的日志记录；`max_multiline_bytes` 和 `max_multiline_lines` 设置为 `-1` 表示不限制。

## 生成协议代码

安装 `protoc-gen-go` 和 `protoc-gen-connect-go` 后执行：

```bash
make generate
```

生成文件已提交，普通构建不需要重新生成。

## Linux 二进制

同时构建 Linux amd64 和 arm64 的 Agent、CLI：

```bash
make build-linux
```

产物位于 `dist/`。也可以分别执行：

```bash
make build-linux-amd64
make build-linux-arm64
```

## 发布

提交全部改动后，执行下面一条命令创建并推送版本标签：

```bash
make tag VERSION=v0.1.0
```

`v*` 标签会触发 GitHub Actions：

- 编译 Linux amd64、Linux arm64 的 Agent 与 CLI，并附加到 GitHub Release。
- 生成所有二进制的 `checksums.txt`。
- 构建并推送 `linux/amd64`、`linux/arm64` 多平台镜像。

镜像发布到当前仓库的 GitHub Container Registry：

```text
ghcr.io/<owner>/<repository>:v0.1.0
ghcr.io/<owner>/<repository>:0.1.0
ghcr.io/<owner>/<repository>:latest
```

拉取指定版本：

```bash
docker pull ghcr.io/<owner>/<repository>:v0.1.0
```

## 开源许可

本项目基于 [MIT License](LICENSE) 开源。
