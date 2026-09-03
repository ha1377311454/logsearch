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

## 5. 检查

```bash
kubectl -n logsearch get pods -o wide
curl http://<node-ip>:30900/healthz
```

Ingress 环境检查：

```bash
curl https://logsearch.example.com/healthz
```

## 6. 权限注意事项

DaemonSet 以 UID `65532` 运行。多数 Kubernetes CRI 日志对非 root 用户可读；如果目标集群日志权限更严格，应通过宿主机 ACL 或专用用户组授予只读权限，不建议直接把容器改成 privileged。
