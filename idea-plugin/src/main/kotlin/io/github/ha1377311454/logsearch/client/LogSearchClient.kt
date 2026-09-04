package io.github.ha1377311454.logsearch.client

import com.google.gson.Gson
import com.google.gson.JsonParser
import io.github.ha1377311454.logsearch.model.NodeConfig
import io.github.ha1377311454.logsearch.model.NodeSearchResult
import io.github.ha1377311454.logsearch.model.SearchRequest
import io.github.ha1377311454.logsearch.model.SearchResponse
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration
import kotlin.system.measureTimeMillis

class LogSearchClient(private val gson: Gson = Gson()) {
    private val httpClient = HttpClient.newBuilder()
        .followRedirects(HttpClient.Redirect.NORMAL)
        .build()

    fun search(node: NodeConfig, token: String, timeoutSeconds: Int, query: SearchRequest): NodeSearchResult {
        var response: SearchResponse? = null
        var failure: String? = null
        val elapsed = measureTimeMillis {
            try {
                val requestBuilder = HttpRequest.newBuilder()
                    .uri(searchUri(node.address))
                    .timeout(Duration.ofSeconds(timeoutSeconds.toLong()))
                    .header("Content-Type", "application/json")
                    .header("Connect-Protocol-Version", "1")
                    .POST(HttpRequest.BodyPublishers.ofString(gson.toJson(query)))
                if (token.isNotBlank()) requestBuilder.header("Authorization", "Bearer $token")

                val httpResponse = httpClient.send(requestBuilder.build(), HttpResponse.BodyHandlers.ofString())
                if (httpResponse.statusCode() !in 200..299) {
                    throw IllegalStateException(errorMessage(httpResponse.body(), httpResponse.statusCode()))
                }
                response = gson.fromJson(httpResponse.body(), SearchResponse::class.java)
            } catch (error: Exception) {
                failure = when (error) {
                    is java.net.http.HttpTimeoutException -> "查询超时"
                    else -> error.message ?: error.javaClass.simpleName
                }
            }
        }
        return NodeSearchResult(node, response, elapsed, failure)
    }

    private fun searchUri(address: String): URI {
        val base = address.trim().trimEnd('/')
        require(base.startsWith("http://") || base.startsWith("https://")) { "节点地址必须以 http:// 或 https:// 开头" }
        return URI.create("$base/logsearch.v1.LogSearchService/Search")
    }

    private fun errorMessage(body: String, statusCode: Int): String = try {
        JsonParser.parseString(body).asJsonObject.get("message")?.asString ?: "HTTP $statusCode"
    } catch (_: Exception) {
        "HTTP $statusCode"
    }
}

