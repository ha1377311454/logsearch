let networkRequests = [];
const $ = (selector) => document.querySelector(selector);

// DevTools 主题可以独立于操作系统主题，优先使用当前 DevTools 的主题值。
if (chrome.devtools?.panels?.themeName) {
  document.documentElement.dataset.theme = chrome.devtools.panels.themeName;
}

const SETTINGS_DEFAULTS = {
  nodes: [],
  token: "",
  timeoutSeconds: 20,
  concurrency: 5,
  requestIdHeaders: ["x-request-id", "x-trace-id", "trace-id", "traceparent"]
};

$("#settings").addEventListener("click", openSettings);
$("#closeSettings").addEventListener("click", closeSettings);
$("#cancelSettings").addEventListener("click", closeSettings);
$("#saveSettings").addEventListener("click", saveSettings);
$("#settingsDialog").addEventListener("click", (event) => {
  if (event.target === $("#settingsDialog")) closeSettings();
});
$("#refresh").addEventListener("click", loadRequests);
$("#requests").addEventListener("change", extractRequestId);
$("#search").addEventListener("click", runSearch);
chrome.devtools.network.onRequestFinished.addListener(() => loadRequests());

async function openSettings() {
  const config = await chrome.storage.local.get(SETTINGS_DEFAULTS);
  $("#settingsNodes").value = config.nodes.map((node) => node.name && node.name !== node.address ? `${node.name} ${node.address}` : node.address).join("\n");
  $("#settingsToken").value = config.token;
  $("#settingsTimeout").value = config.timeoutSeconds;
  $("#settingsConcurrency").value = config.concurrency;
  $("#settingsHeaders").value = config.requestIdHeaders.join(", ");
  $("#settingsStatus").textContent = "";
  $("#settingsStatus").classList.remove("danger");
  $("#settingsDialog").showModal();
  $("#settingsNodes").focus();
}

function closeSettings() {
  $("#settingsDialog").close();
}

async function saveSettings() {
  const nodes = $("#settingsNodes").value.split("\n").map((line) => line.trim()).filter(Boolean).map((line) => {
    const separator = line.search(/\s+/);
    return separator < 0
      ? { name: line, address: line, enabled: true }
      : { name: line.slice(0, separator), address: line.slice(separator).trim(), enabled: true };
  });
  if (!nodes.length) {
    $("#settingsStatus").textContent = "至少配置一个节点";
    $("#settingsStatus").classList.add("danger");
    return;
  }
  const invalidNode = nodes.find((node) => {
    try {
      return !["http:", "https:"].includes(new URL(node.address).protocol);
    } catch {
      return true;
    }
  });
  if (invalidNode) {
    $("#settingsStatus").textContent = `节点地址无效：${invalidNode.address}`;
    $("#settingsStatus").classList.add("danger");
    return;
  }
  await chrome.storage.local.set({
    nodes,
    token: $("#settingsToken").value.trim(),
    timeoutSeconds: Number($("#settingsTimeout").value) || 20,
    concurrency: Number($("#settingsConcurrency").value) || 5,
    requestIdHeaders: $("#settingsHeaders").value.split(",").map((item) => item.trim().toLowerCase()).filter(Boolean)
  });
  $("#settingsStatus").classList.remove("danger");
  $("#settingsStatus").textContent = "已保存";
  setTimeout(closeSettings, 350);
}

function loadRequests() {
  chrome.devtools.network.getHAR((har) => {
    networkRequests = har.entries.slice(-200).reverse();
    const select = $("#requests");
    select.replaceChildren(new Option("选择一个请求", ""));
    networkRequests.forEach((entry, index) => {
      const url = new URL(entry.request.url);
      select.add(new Option(`${entry.request.method} ${url.pathname}  ${entry.response.status}`, String(index)));
    });
  });
}

async function extractRequestId() {
  const index = $("#requests").value;
  if (index === "") return;
  const entry = networkRequests[Number(index)];
  const config = await chrome.storage.local.get({ requestIdHeaders: ["x-request-id", "x-trace-id", "trace-id", "traceparent"] });
  const headers = [...entry.request.headers, ...entry.response.headers];
  for (const preferred of config.requestIdHeaders) {
    const header = headers.find((item) => item.name.toLowerCase() === preferred.toLowerCase());
    if (header?.value) {
      $("#keywords").value = header.value;
      return;
    }
  }
}

async function runSearch() {
  const keywords = $("#keywords").value.split(/[,;\n]/).map((item) => item.trim()).filter(Boolean);
  if (!keywords.length) return showSummary("请输入 Request ID 或关键词", true);
  $("#search").disabled = true;
  $("#results").replaceChildren();
  $("#errors").replaceChildren();
  showSummary("正在查询所有节点…");
  const payload = {
    keywords,
    keywordMode: $("#mode").value,
    beforeContext: Number($("#before").value) || 0,
    afterContext: Number($("#after").value) || 0,
    maxResults: Number($("#maxResults").value) || 500
  };
  try {
    const response = await chrome.runtime.sendMessage({ type: "search", payload });
    if (!response.ok) throw new Error(response.error);
    render(response.data);
  } catch (error) {
    showSummary(String(error.message || error), true);
  } finally {
    $("#search").disabled = false;
  }
}

function render(nodes) {
  const successful = nodes.filter((node) => node.ok);
  const failed = nodes.filter((node) => !node.ok);
  const matches = successful.flatMap((node) => (node.data.matches || []).map((match) => ({ ...match, sourceNode: node.node })));
  const discoveredFiles = successful.reduce((total, node) => total + Number(node.data.discoveredFiles || 0), 0);
  const scannedFiles = successful.reduce((total, node) => total + Number(node.data.scannedFiles || 0), 0);
  const elapsedMs = successful.reduce((maximum, node) => Math.max(maximum, Number(node.data.elapsedMs || node.elapsedMs || 0)), 0);
  const truncated = successful.filter((node) => node.data.truncated);
  matches.sort((a, b) => (a.timestamp || "").localeCompare(b.timestamp || "") || a.sourceNode.localeCompare(b.sourceNode));
  const truncatedText = truncated.length ? `，${truncated.length} 个节点结果被截断` : "";
  showSummary(`${successful.length}/${nodes.length} 个节点成功，发现 ${discoveredFiles} 个文件，扫描 ${scannedFiles} 个，命中 ${matches.length} 条，耗时 ${elapsedMs} ms${truncatedText}`);
  for (const node of failed) {
    const item = document.createElement("div");
    item.className = "error-item";
    item.textContent = `${node.node}: ${node.error}`;
    $("#errors").append(item);
  }
  for (const match of matches) $("#results").append(renderMatch(match));
  if (!matches.length) {
    const empty = document.createElement("div");
    empty.className = "card empty";
    empty.textContent = "没有找到匹配日志";
    $("#results").append(empty);
  }
}

function renderMatch(match) {
  const article = document.createElement("article");
  article.className = "card log-entry";
  const meta = document.createElement("div");
  meta.className = "log-meta";
  meta.textContent = `${match.sourceNode} · ${match.sourceType || "kubelet"} · ${match.namespace || "-"}/${match.pod || "-"}/${match.container || "-"} · ${match.file || "-"} · line ${match.lineNumber}`;
  const pre = document.createElement("pre");
  const lines = [...(match.before || []), match.text, ...(match.after || [])];
  pre.textContent = lines.join("\n");
  const copy = document.createElement("button");
  copy.className = "copy";
  copy.textContent = "复制";
  copy.addEventListener("click", () => navigator.clipboard.writeText(pre.textContent));
  article.append(meta, copy, pre);
  return article;
}

function showSummary(message, error = false) {
  $("#summary").textContent = message;
  $("#summary").classList.toggle("danger", error);
}

loadRequests();
