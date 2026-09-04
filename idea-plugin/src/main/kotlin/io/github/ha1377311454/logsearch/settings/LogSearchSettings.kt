package io.github.ha1377311454.logsearch.settings

import com.intellij.openapi.components.Service
import com.intellij.openapi.components.State
import com.intellij.openapi.components.Storage
import com.intellij.openapi.components.service
import com.intellij.openapi.components.PersistentStateComponent
import io.github.ha1377311454.logsearch.model.EnvironmentConfig
import io.github.ha1377311454.logsearch.model.NodeConfig
import java.util.UUID

data class LogSearchState(
    var activeEnvironmentId: String = "default",
    var environments: MutableList<EnvironmentConfig> = mutableListOf(defaultEnvironment()),
)

private fun defaultEnvironment() = EnvironmentConfig(
    id = "default",
    name = "默认环境",
    nodes = mutableListOf(NodeConfig("agent-01", "http://127.0.0.1:30900")),
)

@Service(Service.Level.APP)
@State(name = "LogSearchSettings", storages = [Storage("logsearch.xml")])
class LogSearchSettings : PersistentStateComponent<LogSearchState> {
    private var state = LogSearchState()

    override fun getState(): LogSearchState = state

    override fun loadState(state: LogSearchState) {
        this.state = state
        normalize()
    }

    fun environments(): List<EnvironmentConfig> = state.environments.map { it.copy(nodes = it.nodes.map(NodeConfig::copy).toMutableList()) }

    fun activeEnvironment(): EnvironmentConfig {
        normalize()
        return state.environments.firstOrNull { it.id == state.activeEnvironmentId } ?: state.environments.first()
    }

    fun saveEnvironment(environment: EnvironmentConfig) {
        val normalized = environment.copy(
            id = environment.id.ifBlank { UUID.randomUUID().toString() },
            name = environment.name.trim(),
            nodes = environment.nodes.map { it.copy(name = it.name.trim(), address = it.address.trim().trimEnd('/')) }.toMutableList(),
            timeoutSeconds = environment.timeoutSeconds.coerceIn(1, 300),
            concurrency = environment.concurrency.coerceIn(1, 20),
        )
        val index = state.environments.indexOfFirst { it.id == normalized.id }
        if (index >= 0) state.environments[index] = normalized else state.environments.add(normalized)
        state.activeEnvironmentId = normalized.id
    }

    fun setActiveEnvironment(id: String) {
        if (state.environments.any { it.id == id }) state.activeEnvironmentId = id
    }

    fun deleteEnvironment(id: String) {
        if (state.environments.size <= 1) return
        state.environments.removeIf { it.id == id }
        normalize()
    }

    private fun normalize() {
        if (state.environments.isEmpty()) state.environments.add(defaultEnvironment())
        if (state.environments.none { it.id == state.activeEnvironmentId }) {
            state.activeEnvironmentId = state.environments.first().id
        }
    }

    companion object {
        fun getInstance(): LogSearchSettings = service()
    }
}

