# LogSearch IDEA 插件

IDEA 插件版只提供 Agent 节点配置和日志搜索，不读取浏览器 Network 请求。用户在 `LogSearch` Tool Window 中手工输入 Request ID 或其他关键词。

## 功能

- 保存多个连接环境，并在环境间切换。
- 每个环境配置多个 Agent 节点，可使用 `#` 临时禁用节点。
- Bearer Token 通过 IntelliJ Platform `PasswordSafe` 保存，不写入 `logsearch.xml`。
- 按环境配置超时和最大并发数，并发查询全部启用节点。
- 支持全部关键词在同一行（ALL）或任一关键词（ANY）。
- 支持前后文、最大结果数、失败节点提示、截断提示、本地结果过滤和长日志折行显示。

## 开发运行

项目使用 JDK 17、Kotlin、Gradle Wrapper 9.0 和 IntelliJ Platform Gradle Plugin。不要直接使用本机的旧版 `gradle`。进入本目录后执行：

```bash
JAVA_HOME=$(/usr/libexec/java_home -v 17) ./gradlew runIde
```

Gradle 9.0 和 IntelliJ Platform Gradle Plugin 2.18.1 均要求使用 JDK 17 或更高版本启动。`jvmToolchain` 只控制插件源码编译使用的 JDK，无法修复由 Java 11 启动 Gradle 时的配置错误。

在启动的沙箱 IDEA 中打开右侧 `LogSearch` Tool Window：

1. 点击“配置节点”。
2. 每行填写一个节点，格式为 `名称 地址`，例如 `node-01 http://127.0.0.1:30900`。
3. 根据部署情况填写 Bearer Token、超时和并发数。
4. 输入 Request ID 或关键词并点击“查询所有节点”。

多个关键词使用逗号、分号或换行分隔。节点地址只填写协议、主机和端口，插件会自动追加：

```text
/logsearch.v1.LogSearchService/Search
```

## 构建安装包

```bash
JAVA_HOME=$(/usr/libexec/java_home -v 17) ./gradlew buildPlugin
```

ZIP 产物位于 `build/distributions/`，可通过 IDEA 的 `Settings | Plugins | Install Plugin from Disk` 安装。
