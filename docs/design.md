# LogSearch 设计文档

## 1. 背景与目标

前端请求 API 时通常携带 `X-Request-ID`、`X-Trace-ID` 或 `traceparent`。同一个标识会写入服务端日志。LogSearch 的目标是在 Chrome DevTools 中取得该标识，并同时查询 Kubernetes 各节点上的容器日志。

首版包含：

- `logsearch-agent`：以 DaemonSet 运行，每个节点只读查询本节点日志。
- `logsearch-cli`：用于协议验证和命令行查询。
- Chrome Manifest V3 DevTools 扩展：提取 Request ID、并发查询节点并聚合展示。
- Kubernetes 部署清单和示例配置。

## 2. 总体架构

```text
Browser Network request
        |
Chrome DevTools extension
        |  Connect protocol over HTTPS (JSON/protobuf)
        +----------+-----------+
        |          |           |
    node-01     node-02      node-N
     agent       agent        agent
        |          |           |
  /var/log/pods on each Kubernetes node
```

Chrome 不能直接连接原生 gRPC TCP 服务。Agent 使用 Connect 协议，一个 HTTP handler 同时兼容 Connect、gRPC 和 gRPC-Web；浏览器扩展通过 Connect JSON 请求访问。

首版由扩展保存节点地址并扇出查询。节点数量较多或地址频繁变化时，可增加中心 Gateway，由 Gateway 发现 Agent 并聚合结果，不需要改变查询核心和 protobuf 模型。

## 3. 查询流程

1. 用户在 DevTools 的 LogSearch 面板中选择一个 Network 请求。
2. 扩展按配置从请求头、响应头中提取 Request ID；未找到时允许手工输入。
3. Service Worker 对所有启用节点执行有并发上限的查询。
4. 每个 Agent 校验请求，在允许的日志根目录中选择候选文件并逐行扫描。
5. 扩展使用 `Promise.allSettled` 语义汇总；单节点失败不影响其他节点。
6. 结果按日志时间、节点、文件和行号排序，并明确显示超时、失败和截断状态。

## 4. 服务端设计

### 4.1 搜索范围

Agent 不接受客户端提供的绝对路径。客户端只能给出 namespace、Pod、容器和文件名 glob。服务端始终将目标限制在配置的 `search.roots` 内，并拒绝符号链接逃逸。

Agent 配置可通过 `search.pod_name_contains` 进一步限制允许读取的 Pod。该规则按 Pod 名进行不区分大小写的包含匹配，多个配置值是 OR 关系，在任何客户端过滤之前执行，因此客户端无法扩大 Agent 配置的日志范围。空数组表示不限制 Pod。

除 kubelet 标准输出日志外，Agent 支持 `search.process_logs`。实现参考 `log-collector`：使用 gopsutil 枚举本节点进程，通过 `comm_regex` 和 `cmdline_regex` 进行 AND 匹配，读取进程当前打开的文件，并优先转换为 `/proc/<pid>/root/<容器内路径>`。配置 `log_dirs` 后，还会从同一容器文件系统目录中发现轮转文件。

进程日志目录和文件规则完全由 Agent 配置，客户端不能传入目录。`include_regex` 约束进程已打开的文件，`file_patterns` 约束指定目录中的文件，`max_files` 按修改时间保留最新文件，`max_file_age` 排除过旧文件。

Kubernetes 默认日志根目录为 `/host/var/log/pods`。文件路径通常包含 namespace、Pod UID 和容器名；Agent同时从路径及日志文件名提取元数据。

### 4.2 关键词语义

- `ALL`：所有关键词必须同时出现在同一行，默认值。
- `ANY`：任意一个关键词出现即可。
- 默认不区分大小写，可由请求修改。
- 关键词按普通字符串匹配，首版不开放正则表达式。

### 4.3 资源控制

服务端配置硬限制：

- 同时处理的查询数量。
- 每次扫描的文件数。
- 最大结果数和最大响应字节数。
- 查询超时。
- 单行最大字节数。

达到结果数、字节数或超时限制时返回 `truncated=true`，而不是继续消耗节点资源。

### 4.4 协议

协议定义位于 `api/logsearch/v1/logsearch.proto`：

- `Health`：存活状态和节点名。
- `ListLogFiles`：受控地列出候选日志，主要用于诊断。
- `Search`：查询日志并返回命中结果。

首版 `Search` 使用 unary 响应，受最大结果数和字节数保护。后续日志量确实需要时，可以增加 `SearchStream`，不改变当前接口。

## 5. Kubernetes 部署

Agent 使用 DaemonSet，每个节点一份副本，只读挂载宿主机 `/var/log` 到 `/host/var/log`。容器无需 privileged，也不需要 Kubernetes API RBAC。

示例通过 NodePort 暴露，适用于受控内网验证。生产环境推荐在 Agent 前增加 TLS Ingress/Gateway，并通过 NetworkPolicy 限制来源。

## 6. Chrome 扩展

扩展由以下部分组成：

- DevTools page：注册 LogSearch 面板。
- Panel：读取 Network 请求、触发查询、过滤和展示结果。
- Service Worker：保存配置并完成跨节点请求。
- Options page：配置节点地址、Token、超时、并发数和 Request ID 请求头。

扩展使用 `chrome.storage.local` 保存配置。Token 只保存在本机扩展存储中，不写入源码；Agent 日志不得打印 Token。

## 7. 安全设计

- 日志目录白名单和最终路径归属校验。
- Bearer Token 可选认证；生产必须配合 HTTPS。
- 请求体大小限制、查询超时、并发限制和返回量限制。
- 非 root、只读根文件系统、只读 HostPath。
- 默认不返回任意文件，不执行 shell 命令。
- CORS 只允许配置的来源；扩展直连时可配置 `chrome-extension://<id>`。

## 8. 项目结构

```text
api/logsearch/v1/       protobuf 和生成代码
cmd/logsearch-agent/    节点服务
cmd/logsearch-cli/      命令行客户端
internal/config/        YAML 配置
internal/search/        文件选择和逐行搜索
internal/server/        Connect handler、安全与 CORS
extension/              Chrome DevTools 扩展
deploy/                 Kubernetes 清单
configs/                示例配置
docs/                   架构、部署和使用说明
```

## 9. 首版验收标准

- 能从 DevTools 选中的请求头或响应头提取 Request ID。
- 能配置多个 Agent，并发查询且隔离单节点故障。
- `ALL` 模式要求全部关键词处于同一行。
- 返回节点、namespace、Pod、容器、文件、行号和上下文。
- 返回 `kubelet` 或 `process` 日志来源及对应 Agent 规则名。
- 超过限制时明确标记截断。
- 无法通过绝对路径、`..` 或符号链接读取白名单目录之外的文件。
- DaemonSet 能以只读方式访问 `/var/log/pods`。

## 10. 后续演进

- 增加 Gateway 和 Kubernetes 节点自动发现。
- 流式查询及取消传播。
- `.gz` 轮转日志查询。
- 基于时间范围预过滤 CRI 日志。
- mTLS、OIDC 和查询审计。
- 敏感字段脱敏规则。
