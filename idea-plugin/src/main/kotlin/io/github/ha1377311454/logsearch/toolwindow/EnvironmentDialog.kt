package io.github.ha1377311454.logsearch.toolwindow

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.ui.DialogWrapper
import com.intellij.openapi.ui.ValidationInfo
import com.intellij.ui.components.JBLabel
import com.intellij.ui.components.JBScrollPane
import com.intellij.ui.components.JBTextArea
import com.intellij.ui.components.JBTextField
import com.intellij.util.concurrency.AppExecutorUtil
import io.github.ha1377311454.logsearch.model.EnvironmentConfig
import io.github.ha1377311454.logsearch.model.NodeConfig
import io.github.ha1377311454.logsearch.settings.LogSearchSettings
import io.github.ha1377311454.logsearch.settings.TokenStore
import java.awt.BorderLayout
import java.awt.GridBagConstraints
import java.awt.GridBagLayout
import java.awt.Insets
import javax.swing.JComponent
import javax.swing.JPanel
import javax.swing.JSpinner
import javax.swing.SpinnerNumberModel
import javax.swing.event.DocumentEvent
import javax.swing.event.DocumentListener

class EnvironmentDialog(
    private val environment: EnvironmentConfig,
    private val onSaved: () -> Unit,
) : DialogWrapper(true) {
    private val nodesArea = JBTextArea(9, 55)
    private val timeoutSpinner = JSpinner(SpinnerNumberModel(environment.timeoutSeconds, 1, 300, 1))
    private val concurrencySpinner = JSpinner(SpinnerNumberModel(environment.concurrency, 1, 20, 1))
    private val tokenField = JBTextField()
    private var tokenLoaded = false
    private var tokenEdited = false
    private var applyingLoadedToken = false

    init {
        title = "LogSearch 节点配置 - ${environment.name}"
        nodesArea.text = environment.nodes.joinToString("\n") { node ->
            val prefix = if (node.enabled) "" else "# "
            "$prefix${node.name} ${node.address}".trim()
        }
        init()
        tokenField.emptyText.text = "正在读取；不修改则保留原 Token"
        tokenField.document.addDocumentListener(object : DocumentListener {
            override fun insertUpdate(event: DocumentEvent) = markTokenEdited()
            override fun removeUpdate(event: DocumentEvent) = markTokenEdited()
            override fun changedUpdate(event: DocumentEvent) = markTokenEdited()
        })
        AppExecutorUtil.getAppExecutorService().execute {
            val token = TokenStore.get(environment.id)
            ApplicationManager.getApplication().invokeLater {
                if (!isDisposed) {
                    tokenLoaded = true
                    if (!tokenEdited) {
                        applyingLoadedToken = true
                        tokenField.text = token
                        applyingLoadedToken = false
                        tokenEdited = false
                    }
                    tokenField.emptyText.text = "无鉴权时留空"
                }
            }
        }
    }

    override fun createCenterPanel(): JComponent {
        val panel = JPanel(GridBagLayout())
        val constraints = GridBagConstraints().apply {
            anchor = GridBagConstraints.WEST
            fill = GridBagConstraints.HORIZONTAL
            weightx = 1.0
            insets = Insets(4, 4, 4, 4)
        }
        fun addRow(row: Int, label: String, component: JComponent, fill: Int = GridBagConstraints.HORIZONTAL, weightY: Double = 0.0) {
            constraints.gridx = 0
            constraints.gridy = row
            constraints.weighty = 0.0
            constraints.fill = GridBagConstraints.NONE
            panel.add(JBLabel(label), constraints)
            constraints.gridx = 1
            constraints.weighty = weightY
            constraints.fill = fill
            panel.add(component, constraints)
        }

        val help = JBLabel("每行格式：名称 地址；以 # 开头表示禁用")
        val nodesPanel = JPanel(BorderLayout(0, 4)).apply {
            add(JBScrollPane(nodesArea), BorderLayout.CENTER)
            add(help, BorderLayout.SOUTH)
        }
        addRow(0, "Agent 节点", nodesPanel, GridBagConstraints.BOTH, 1.0)
        addRow(1, "Bearer Token", tokenField)
        addRow(2, "超时（秒）", timeoutSpinner)
        addRow(3, "并发数", concurrencySpinner)
        return panel
    }

    override fun doValidate(): ValidationInfo? {
        val nodes = parseNodes()
        if (nodes.isEmpty()) return ValidationInfo("至少配置一个节点", nodesArea)
        val invalid = nodes.firstOrNull { node ->
            !node.address.startsWith("http://") && !node.address.startsWith("https://")
        }
        return invalid?.let { ValidationInfo("节点地址必须以 http:// 或 https:// 开头：${it.address}", nodesArea) }
    }

    override fun doOKAction() {
        val updated = environment.copy(
            nodes = parseNodes().toMutableList(),
            timeoutSeconds = timeoutSpinner.value as Int,
            concurrency = concurrencySpinner.value as Int,
        )
        LogSearchSettings.getInstance().saveEnvironment(updated)
        if (tokenLoaded || tokenEdited) {
            val token = tokenField.text.trim()
            AppExecutorUtil.getAppExecutorService().execute { TokenStore.set(environment.id, token) }
        }
        super.doOKAction()
        onSaved()
    }

    private fun markTokenEdited() {
        if (!applyingLoadedToken) tokenEdited = true
    }

    private fun parseNodes(): List<NodeConfig> = nodesArea.text.lineSequence().mapNotNull { rawLine ->
        val trimmed = rawLine.trim()
        if (trimmed.isBlank()) return@mapNotNull null
        val enabled = !trimmed.startsWith('#')
        val line = trimmed.removePrefix("#").trim()
        val parts = line.split(Regex("\\s+"), limit = 2)
        val address = if (parts.size == 1) parts[0] else parts[1]
        val name = if (parts.size == 1) address else parts[0]
        NodeConfig(name, address.trimEnd('/'), enabled)
    }.toList()
}
