# Chrome 插件使用说明

## 安装

1. 打开 `chrome://extensions`。
2. 开启“开发者模式”。
3. 点击“加载已解压的扩展程序”。
4. 选择本项目的 `extension` 目录。

## 配置

在扩展详情中打开“扩展程序选项”，每行配置一个节点：

```text
node-01 https://node-01.example.internal:9443
node-02 https://node-02.example.internal:9443
```

然后填写与 Kubernetes Secret 一致的 Bearer Token。节点地址只填写协议、主机和端口，不要添加 `/logsearch.v1.LogSearchService/Search`。

## 查询

1. 打开目标页面的 DevTools。
2. 切换到 `LogSearch` 面板。
3. 点击“刷新请求”，选择一个 Network 请求。
4. 插件按设置的请求头优先级提取 Request ID。
5. 确认关键词，点击“查询所有节点”。

支持逗号、分号或换行分隔多个关键词。默认“全部关键词在同一行”；任一节点失败或超时不会影响其他节点的结果。

## 限制

- 首版只从请求头和响应头提取 Request ID，不解析响应 JSON。
- Chrome 必须信任服务端 HTTPS 证书。
- 如果更换扩展 ID，应同步收紧 Agent 的 `allowed_origins` 配置。
