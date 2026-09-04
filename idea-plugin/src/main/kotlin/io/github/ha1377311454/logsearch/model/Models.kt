package io.github.ha1377311454.logsearch.model

data class NodeConfig(
    var name: String = "",
    var address: String = "",
    var enabled: Boolean = true,
)

data class EnvironmentConfig(
    var id: String = "",
    var name: String = "",
    var nodes: MutableList<NodeConfig> = mutableListOf(),
    var timeoutSeconds: Int = 20,
    var concurrency: Int = 5,
)

data class SearchRequest(
    val keywords: List<String>,
    val keywordMode: String,
    val beforeContext: Int,
    val afterContext: Int,
    val maxResults: Int,
)

data class SearchResponse(
    val matches: List<LogMatch> = emptyList(),
    val scannedFiles: Int = 0,
    val scannedBytes: Long = 0,
    val truncated: Boolean = false,
    val truncationReason: String = "",
    val elapsedMs: Long = 0,
    val discoveredFiles: Int = 0,
)

data class LogMatch(
    val nodeName: String = "",
    val namespace: String = "",
    val pod: String = "",
    val container: String = "",
    val file: String = "",
    val lineNumber: Long = 0,
    val timestamp: String = "",
    val text: String = "",
    val before: List<String> = emptyList(),
    val after: List<String> = emptyList(),
    val sourceType: String = "",
    val sourceRule: String = "",
)

data class NodeSearchResult(
    val node: NodeConfig,
    val response: SearchResponse? = null,
    val elapsedMs: Long = 0,
    val error: String? = null,
) {
    val successful: Boolean get() = error == null && response != null
}

data class DisplayMatch(
    val sourceNode: String,
    val match: LogMatch,
)

