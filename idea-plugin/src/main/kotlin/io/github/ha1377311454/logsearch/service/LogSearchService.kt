package io.github.ha1377311454.logsearch.service

import io.github.ha1377311454.logsearch.client.LogSearchClient
import io.github.ha1377311454.logsearch.model.EnvironmentConfig
import io.github.ha1377311454.logsearch.model.NodeSearchResult
import io.github.ha1377311454.logsearch.model.SearchRequest
import java.util.concurrent.CompletableFuture
import java.util.concurrent.Executors

class LogSearchService(private val client: LogSearchClient = LogSearchClient()) {
    fun searchAll(environment: EnvironmentConfig, token: String, query: SearchRequest): CompletableFuture<List<NodeSearchResult>> {
        val nodes = environment.nodes.filter { it.enabled && it.address.isNotBlank() }
        require(nodes.isNotEmpty()) { "当前环境没有启用的节点" }
        val executor = Executors.newFixedThreadPool(environment.concurrency.coerceAtMost(nodes.size))
        val tasks = nodes.map { node ->
            CompletableFuture.supplyAsync(
                { client.search(node, token, environment.timeoutSeconds, query) },
                executor,
            )
        }
        return CompletableFuture.allOf(*tasks.toTypedArray())
            .thenApply { tasks.map(CompletableFuture<NodeSearchResult>::join) }
            .whenComplete { _, _ -> executor.shutdown() }
    }
}

