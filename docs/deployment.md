# Kubernetes 部署说明

## 1. 构建镜像

```bash
docker build -t registry.example.com/logsearch-agent:0.1.0 .
docker push registry.example.com/logsearch-agent:0.1.0
```

将目标环境清单中的 `ghcr.io/OWNER/REPOSITORY:v0.1.0` 替换为实际镜像地址。

## 2. 开发环境：NodePort、无鉴权

开发环境清单是 `deploy/logsearch-dev.yaml`，默认：

- 使用 NodePort `30900`。
- 使用 `externalTrafficPolicy: Local`，访问某个节点 IP 时只查询该节点 Agent。
- 不启用 Token 鉴权。
- 不创建 Ingress 和 NetworkPolicy。

部署：

```bash
kubectl apply -f deploy/logsearch-dev.yaml
kubectl -n logsearch rollout status daemonset/logsearch-agent
```

插件节点地址配置为：

```text
http://<node-01-ip>:30900
http://<node-02-ip>:30900
```

## 3. 鉴权和 Ingress 环境

带鉴权和 Ingress 的清单是 `deploy/logsearch-auth-ingress.yaml`。部署前必须修改：

- 镜像地址 `ghcr.io/OWNER/REPOSITORY:v0.1.0`。
- Token `REPLACE_WITH_A_LONG_RANDOM_TOKEN`。
- Ingress 域名 `logsearch.example.com`。
- TLS Secret `logsearch-tls`。
- NetworkPolicy 允许的 Ingress Controller 来源网段。

部署：

```bash
kubectl apply -f deploy/logsearch-auth-ingress.yaml
kubectl -n logsearch rollout status daemonset/logsearch-agent
```

注意：普通 Ingress 后面的 ClusterIP Service 会在多个 Agent Pod 之间负载均衡，一次请求只会查询其中一个节点。若要通过单一 Ingress 地址查询所有节点，需要后续增加聚合 Gateway；当前“查询每个节点”的完整语义由开发环境 NodePort 清单提供。

## 4. Agent 端 Pod 日志范围

两套清单都可以配置：

```yaml
search:
  pod_name_contains:
    - order-api
    - payment-service
```

以上配置会匹配 Pod 名中包含 `order-api` 或 `payment-service` 的日志，不区分大小写。空数组 `[]` 表示允许所有 Pod。该范围由 Agent 强制执行，浏览器插件和 CLI 无法绕过。

## 5. 容器进程日志

对于没有写到 stdout、而是写入容器文件系统的日志，配置 `process_logs`：

```yaml
search:
  process_logs:
    - name: example-api
      comm_regex: '^java$'
      cmdline_regex: 'example-api'
      include_regex: '^/var/log/example-api/example-api(?:\..+)?\.log$'
      log_dirs:
        - /var/log/example-api
      file_patterns:
        - 'example-api*.log'
      max_files: 20
      max_file_age: 48h
```

查询流程是：

1. 枚举节点进程，`comm_regex` 和 `cmdline_regex` 同时命中才继续。
2. 从进程打开文件中发现当前日志，例如 `example-api.log`。
3. 通过 `/proc/<pid>/root` 映射到该进程所在容器的文件系统。
4. 扫描 `log_dirs` 中符合 `file_patterns` 的轮转日志。
5. 按修改时间倒序，只保留最近 `max_files` 个且未超过 `max_file_age` 的文件。

DaemonSet 必须启用 `hostPID: true` 并拥有 `SYS_PTRACE` capability。仍然保持 `privileged: false`，日志目录也没有以可写方式挂载。

## 6. 检查

```bash
kubectl -n logsearch get pods -o wide
curl http://<node-ip>:30900/healthz
```

Ingress 环境检查：

```bash
curl https://logsearch.example.com/healthz
```

## 7. 权限注意事项

DaemonSet 明确保持 `privileged: false`，但为兼容权限严格的 kubelet/CRI 日志，容器以 UID `0` 运行并按宿主机采集器模式增加 `DAC_OVERRIDE`、`DAC_READ_SEARCH` 等 capabilities。容器根文件系统和宿主机 `/var/log` 仍然使用 `readOnly: true`，因此 Agent 不能修改宿主机日志。不要将容器改成 privileged，也不要挂载整个宿主机根目录。

在开启 SELinux Enforcing 的 CentOS/RHEL 节点上，仅增加 `DAC_READ_SEARCH` 仍可能被 SELinux 拒绝。清单额外设置 `seLinuxOptions.type: spc_t`，允许这个非特权宿主机采集器访问只读 hostPath。该配置没有启用 privileged，其他安全限制和只读挂载仍然生效。如果集群启用了 Restricted Pod Security 并禁止 `spc_t`，则需要由集群管理员为日志目录和 Agent 定义专用 SELinux 策略。
