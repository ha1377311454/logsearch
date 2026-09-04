# Chrome 插件使用说明

## 安装

1. 打开 `chrome://extensions`。
2. 开启“开发者模式”。
3. 点击“加载已解压的扩展程序”。
4. 选择本项目的 `extension` 目录。

## 配置

打开 DevTools 的 `LogSearch` 面板，点击右上角“连接设置”。设置会在当前面板内弹出，不会跳转到独立扩展页面。每行配置一个节点：

```text
node-01 https://node-01.example.internal:9443
node-02 https://node-02.example.internal:9443
```

然后填写与 Kubernetes Secret 一致的 Bearer Token。节点地址只填写协议、主机和端口，不要添加 `/logsearch.v1.LogSearchService/Search`。

可以为不同集群新建独立的连接环境。选择一个环境后点击“设为默认”，该环境会显示“（默认）”标识，并在下次打开 LogSearch 面板时自动启用；临时切换环境不会改变默认环境。

## 查询

1. 打开目标页面的 DevTools。
2. 切换到 `LogSearch` 面板，左侧会像 Network 一样列出最近的请求。
3. 可以按名称、路径、方法或状态码筛选，然后点击一个请求；点击列表左上角的清空按钮后，只显示此后产生的新请求。
4. 插件先按设置的请求头优先级提取 Request ID；请求头中没有时，再从响应 JSON 的 `requestId`、`request_id`、`traceId` 或 `trace_id` 字段提取。
5. 确认关键词，点击“查询所有节点”。

支持逗号、分号或换行分隔多个关键词。默认“全部关键词在同一行”；任一节点失败或超时不会影响其他节点的结果。

查询完成后，结果区顶部会显示“快捷过滤”。它只过滤本次已经返回到浏览器的结果，不会再次请求 Agent；多个关键词使用空格、逗号或分号分隔，并按 AND 关系匹配。过滤范围包含节点、Namespace、Pod、容器、文件名和日志正文，命中关键词会在结果中高亮。点击单条结果的“复制”后，按钮会显示复制成功或失败状态。

### 常用关键词

在 Request ID 输入框中输入附加关键词后，可以点击“收藏关键词”，单独设置收藏名称和关键词内容。也可以先选中输入框中的一段文本再收藏，弹窗会自动带入选中内容。

收藏项显示在输入框下方。点击收藏项时，关键词会以逗号分隔追加到当前 Request ID 后面，不会覆盖 Request ID；已经存在的关键词会自动去重。每个收藏项可以包含一个或多个关键词，最多保留 30 条，数据存储在 Chrome 本地存储中。

### 请求过滤规则

请求列表顶部支持类似 Network 的组合过滤语法。多个条件使用空格分隔并按 AND 关系匹配，条件前加 `-` 表示排除：

```text
method:POST status:2xx -domain:static.example.com
domain:api.example.com path:/overview
status:>=400
```

支持的字段包括 `method`、`status`、`domain`、`url`、`path`、`name`、`type`、`scheme` 和 `header`。不带字段名的普通文本会同时匹配请求名称、URL、方法、状态和请求头。`status` 支持精确状态码、`2xx` 分组以及 `>=400` 等比较表达式。

输入规则后点击星形按钮可以填写名称并收藏，最多保留 20 条。收藏规则存储在 Chrome 本地存储中，点击规则标签即可应用，点击标签后的 `×` 可删除。

## 限制

- 响应体必须是有效 JSON，插件才能从其中递归提取 Request ID；非 JSON 响应仍只检查请求头和响应头。
- 请求列表保留最近 200 条 Network 请求。
- Chrome 必须信任服务端 HTTPS 证书。
- 如果更换扩展 ID，应同步收紧 Agent 的 `allowed_origins` 配置。
