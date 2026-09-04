package io.github.ha1377311454.logsearch.toolwindow

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.ide.CopyPasteManager
import com.intellij.openapi.project.Project
import com.intellij.openapi.ui.Messages
import com.intellij.ui.JBColor
import com.intellij.ui.components.JBLabel
import com.intellij.ui.components.JBScrollPane
import com.intellij.ui.components.JBTextArea
import com.intellij.ui.components.JBTextField
import com.intellij.util.concurrency.AppExecutorUtil
import io.github.ha1377311454.logsearch.model.DisplayMatch
import io.github.ha1377311454.logsearch.model.EnvironmentConfig
import io.github.ha1377311454.logsearch.model.SearchRequest
import io.github.ha1377311454.logsearch.model.NodeSearchResult
import io.github.ha1377311454.logsearch.service.LogSearchService
import io.github.ha1377311454.logsearch.settings.LogSearchSettings
import io.github.ha1377311454.logsearch.settings.TokenStore
import java.awt.BorderLayout
import java.awt.FlowLayout
import java.awt.Font
import java.awt.datatransfer.StringSelection
import java.util.UUID
import java.util.concurrent.CompletableFuture
import javax.swing.BorderFactory
import javax.swing.BoxLayout
import javax.swing.JButton
import javax.swing.JComboBox
import javax.swing.JComponent
import javax.swing.JPanel
import javax.swing.JSpinner
import javax.swing.JToggleButton
import javax.swing.SpinnerNumberModel
import javax.swing.event.DocumentEvent
import javax.swing.event.DocumentListener

class LogSearchPanel(private val project: Project) {
    private val settings = LogSearchSettings.getInstance()
    private val searchService = LogSearchService()
    private val environmentBox = JComboBox<EnvironmentItem>()
    private val keywordsField = JBTextField()
    private val modeBox = JComboBox(arrayOf("全部关键词在同一行", "任一关键词"))
    private val beforeSpinner = JSpinner(SpinnerNumberModel(0, 0, 100, 1))
    private val afterSpinner = JSpinner(SpinnerNumberModel(0, 0, 100, 1))
    private val maxResultsSpinner = JSpinner(SpinnerNumberModel(5000, 1, 50000, 100))
    private val searchButton = JButton("查询所有节点")
    private val statusLabel = JBLabel("请输入 Request ID 或关键词")
    private val errorArea = JBTextArea()
    private val filterField = JBTextField()
    private val resultArea = JBTextArea()
    private val lineWrapButton = JToggleButton("折行显示")
    private var matches = emptyList<DisplayMatch>()
    private var runningSearch: CompletableFuture<*>? = null
    val component: JComponent = buildUi()

    init {
        refreshEnvironments()
        wireActions()
    }

    private fun buildUi(): JComponent {
        val root = JPanel(BorderLayout(0, 8)).apply { border = BorderFactory.createEmptyBorder(8, 8, 8, 8) }
        val environmentRow = JPanel(FlowLayout(FlowLayout.LEFT, 6, 0)).apply {
            add(JBLabel("环境"))
            add(environmentBox)
            add(JButton("新建").apply { addActionListener { createEnvironment() } })
            add(JButton("删除").apply { addActionListener { deleteEnvironment() } })
            add(JButton("配置节点").apply { addActionListener { configureEnvironment() } })
        }

        keywordsField.emptyText.text = "多个关键词使用逗号、分号或换行分隔"
        filterField.emptyText.text = "过滤已返回结果，多个关键词按 AND 匹配"
        val queryRow = JPanel(FlowLayout(FlowLayout.LEFT, 6, 0)).apply {
            add(JBLabel("匹配方式")); add(modeBox)
            add(JBLabel("前文")); add(beforeSpinner)
            add(JBLabel("后文")); add(afterSpinner)
            add(JBLabel("最大结果")); add(maxResultsSpinner)
            add(searchButton)
        }
        val top = JPanel().apply {
            layout = BoxLayout(this, BoxLayout.Y_AXIS)
            add(environmentRow)
            add(JPanel(BorderLayout(6, 0)).apply {
                border = BorderFactory.createEmptyBorder(8, 0, 8, 0)
                add(JBLabel("Request ID / 关键词"), BorderLayout.WEST)
                add(keywordsField, BorderLayout.CENTER)
            })
            add(queryRow)
            add(JPanel(BorderLayout()).apply {
                border = BorderFactory.createEmptyBorder(8, 4, 0, 4)
                add(statusLabel, BorderLayout.CENTER)
            })
        }

        errorArea.apply {
            isEditable = false
            isOpaque = false
            foreground = JBColor.RED
            lineWrap = true
            wrapStyleWord = true
            isVisible = false
        }
        resultArea.apply {
            isEditable = false
            font = Font(Font.MONOSPACED, Font.PLAIN, font.size)
            lineWrap = false
        }
        val resultsPanel = JPanel(BorderLayout(0, 6)).apply {
            add(JPanel(BorderLayout(6, 0)).apply {
                add(filterField, BorderLayout.CENTER)
                add(JPanel(FlowLayout(FlowLayout.RIGHT, 6, 0)).apply {
                    add(lineWrapButton)
                    add(JButton("复制所选").apply { addActionListener { copySelected() } })
                }, BorderLayout.EAST)
            }, BorderLayout.NORTH)
            add(JBScrollPane(resultArea), BorderLayout.CENTER)
        }
        val content = JPanel(BorderLayout(0, 6)).apply {
            add(errorArea, BorderLayout.NORTH)
            add(resultsPanel, BorderLayout.CENTER)
        }
        root.add(top, BorderLayout.NORTH)
        root.add(content, BorderLayout.CENTER)
        return root
    }

    private fun wireActions() {
        environmentBox.addActionListener {
            val selected = environmentBox.selectedItem as? EnvironmentItem ?: return@addActionListener
            settings.setActiveEnvironment(selected.id)
        }
        searchButton.addActionListener { runSearch() }
        keywordsField.addActionListener { runSearch() }
        lineWrapButton.addActionListener {
            val enabled = lineWrapButton.isSelected
            resultArea.lineWrap = enabled
            resultArea.wrapStyleWord = enabled
            lineWrapButton.text = if (enabled) "取消折行" else "折行显示"
            resultArea.revalidate()
            resultArea.repaint()
        }
        filterField.document.addDocumentListener(object : DocumentListener {
            override fun insertUpdate(e: DocumentEvent) = renderMatches()
            override fun removeUpdate(e: DocumentEvent) = renderMatches()
            override fun changedUpdate(e: DocumentEvent) = renderMatches()
        })
    }

    private fun runSearch() {
        val keywords = splitKeywords(keywordsField.text)
        if (keywords.isEmpty()) {
            statusLabel.text = "请输入 Request ID 或关键词"
            return
        }
        val environment = settings.activeEnvironment()
        val request = SearchRequest(
            keywords = keywords,
            keywordMode = if (modeBox.selectedIndex == 0) "KEYWORD_MODE_ALL" else "KEYWORD_MODE_ANY",
            beforeContext = beforeSpinner.value as Int,
            afterContext = afterSpinner.value as Int,
            maxResults = maxResultsSpinner.value as Int,
        )
        searchButton.isEnabled = false
        statusLabel.text = "正在查询 ${environment.nodes.count { it.enabled }} 个节点…"
        errorArea.text = ""
        errorArea.isVisible = false
        resultArea.text = ""
        matches = emptyList()

        val future = CompletableFuture.supplyAsync(
            { TokenStore.get(environment.id) },
            AppExecutorUtil.getAppExecutorService(),
        ).thenCompose { token -> searchService.searchAll(environment, token, request) }
        runningSearch = future
        future.whenComplete { results, error ->
            ApplicationManager.getApplication().invokeLater {
                if (project.isDisposed || runningSearch !== future) return@invokeLater
                searchButton.isEnabled = true
                if (error != null) {
                    statusLabel.text = error.cause?.message ?: error.message ?: "查询失败"
                    return@invokeLater
                }
                showResults(results)
            }
        }
    }

    private fun showResults(results: List<NodeSearchResult>) {
        val successful = results.filter(NodeSearchResult::successful)
        val failed = results.filterNot(NodeSearchResult::successful)
        matches = successful.flatMap { result ->
            result.response.orEmptyMatches().map { DisplayMatch(result.node.name.ifBlank { result.node.address }, it) }
        }.sortedWith(compareBy({ it.match.timestamp }, { it.sourceNode }, { it.match.file }, { it.match.lineNumber }))
        val discovered = successful.sumOf { it.response?.discoveredFiles ?: 0 }
        val scanned = successful.sumOf { it.response?.scannedFiles ?: 0 }
        val elapsed = successful.maxOfOrNull { it.response?.elapsedMs ?: it.elapsedMs } ?: 0
        val truncated = successful.count { it.response?.truncated == true }
        statusLabel.text = buildString {
            append("${successful.size}/${results.size} 个节点成功，发现 $discovered 个文件，扫描 $scanned 个，命中 ${matches.size} 条，耗时 $elapsed ms")
            if (truncated > 0) append("，$truncated 个节点结果被截断")
        }
        errorArea.text = failed.joinToString("\n") { "${it.node.name.ifBlank { it.node.address }}: ${it.error}" }
        errorArea.isVisible = failed.isNotEmpty()
        renderMatches()
    }

    private fun renderMatches() {
        val terms = filterField.text.trim().split(Regex("[\\s,;]+"), limit = 0).filter(String::isNotBlank)
        val visible = matches.filter { display ->
            val text = searchableText(display)
            terms.all { text.contains(it, ignoreCase = true) }
        }
        resultArea.text = if (visible.isEmpty()) {
            if (matches.isEmpty()) "没有找到匹配日志" else "返回结果中没有匹配项"
        } else {
            visible.joinToString("\n\n${"─".repeat(80)}\n\n", transform = ::formatMatch)
        }
        resultArea.caretPosition = 0
    }

    private fun formatMatch(display: DisplayMatch): String {
        val match = display.match
        val header = "${display.sourceNode} · ${match.sourceType.ifBlank { "kubelet" }} · " +
            "${match.namespace.ifBlank { "-" }}/${match.pod.ifBlank { "-" }}/${match.container.ifBlank { "-" }} · " +
            "${match.file.ifBlank { "-" }}:${match.lineNumber}"
        val timestamp = match.timestamp.takeIf(String::isNotBlank)?.let { "$it\n" }.orEmpty()
        return "$header\n$timestamp${(match.before + match.text + match.after).joinToString("\n")}"
    }

    private fun searchableText(display: DisplayMatch): String = with(display.match) {
        listOf(display.sourceNode, sourceType, sourceRule, namespace, pod, container, file, timestamp, text) + before + after
    }.joinToString("\n")

    private fun copySelected() {
        val text = resultArea.selectedText?.takeIf(String::isNotBlank) ?: resultArea.text
        CopyPasteManager.getInstance().setContents(StringSelection(text))
        statusLabel.text = "已复制结果"
    }

    private fun configureEnvironment() {
        EnvironmentDialog(settings.activeEnvironment(), ::refreshEnvironments).show()
    }

    private fun createEnvironment() {
        val name = Messages.showInputDialog(project, "请输入环境名称", "新建 LogSearch 环境", null)?.trim().orEmpty()
        if (name.isBlank()) return
        val environment = EnvironmentConfig(id = UUID.randomUUID().toString(), name = name)
        settings.saveEnvironment(environment)
        refreshEnvironments()
        configureEnvironment()
    }

    private fun deleteEnvironment() {
        val environment = settings.activeEnvironment()
        if (settings.environments().size <= 1) {
            Messages.showInfoMessage(project, "至少保留一个环境", "LogSearch")
            return
        }
        if (Messages.showYesNoDialog(project, "确定删除环境“${environment.name}”吗？", "删除环境", null) != Messages.YES) return
        settings.deleteEnvironment(environment.id)
        AppExecutorUtil.getAppExecutorService().execute { TokenStore.remove(environment.id) }
        refreshEnvironments()
    }

    private fun refreshEnvironments() {
        val activeId = settings.activeEnvironment().id
        environmentBox.removeAllItems()
        settings.environments().forEach { environmentBox.addItem(EnvironmentItem(it.id, it.name)) }
        (0 until environmentBox.itemCount)
            .firstOrNull { environmentBox.getItemAt(it).id == activeId }
            ?.let { environmentBox.selectedIndex = it }
    }

    private fun splitKeywords(value: String): List<String> =
        value.split(Regex("[,;\\n]")).map(String::trim).filter(String::isNotBlank)

    private fun io.github.ha1377311454.logsearch.model.SearchResponse?.orEmptyMatches() = this?.matches.orEmpty()

    private data class EnvironmentItem(val id: String, val name: String) {
        override fun toString(): String = name
    }
}
