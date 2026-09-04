let networkRequests = [];
let filterFavorites = [];
let keywordFavorites = [];
let requestRefreshTimer;
let requestClearTime = 0;
let selectedRequestKey = "";
let environments = [];
let activeEnvironmentId = "";
let defaultEnvironmentId = "";
let renderedMatches = [];
let resultSummary = "";
let activeResultHighlightIndex = -1;
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
  requestIdHeaders: ["x-request-id", "x-trace-id", "trace-id", "traceparent"],
  requestFilterFavorites: [],
  keywordFavorites: []
};

const ENVIRONMENT_DEFAULTS = {
  environments: [],
  activeEnvironmentId: "",
  defaultEnvironmentId: "",
  environmentDefaultsVersion: 0
};

const CURRENT_ENVIRONMENT_DEFAULTS_VERSION = 1;

const DEFAULT_ENVIRONMENT_CONFIG = {
  nodes: [{ name: "agent-01", address: "http://127.0.0.1:30900", enabled: true }],
  token: "",
  timeoutSeconds: 20,
  concurrency: 5,
  requestIdHeaders: ["x-request-id", "x-trace-id", "trace-id", "traceparent"]
};

$("#settings").addEventListener("click", openSettings);
$("#closeSettings").addEventListener("click", closeSettings);
$("#cancelSettings").addEventListener("click", closeSettings);
$("#saveSettings").addEventListener("click", saveSettings);
$("#quickNewEnvironment").addEventListener("click", () => openEnvironmentDialog(false));
$("#newEnvironment").addEventListener("click", () => openEnvironmentDialog(true));
$("#deleteEnvironment").addEventListener("click", deleteEnvironment);
$("#setDefaultEnvironment").addEventListener("click", setDefaultEnvironment);
$("#environmentSwitcher").addEventListener("change", (event) => switchEnvironment(event.target.value));
$("#settingsEnvironment").addEventListener("change", (event) => switchEnvironment(event.target.value, true));
$("#closeEnvironmentDialog").addEventListener("click", closeEnvironmentDialog);
$("#cancelEnvironment").addEventListener("click", closeEnvironmentDialog);
$("#environmentForm").addEventListener("submit", createEnvironment);
$("#exportConfig").addEventListener("click", exportConfig);
$("#importConfig").addEventListener("click", () => $("#configFile").click());
$("#configFile").addEventListener("change", importConfig);
$("#resultFilter").addEventListener("input", () => {
  activeResultHighlightIndex = -1;
  renderFilteredResults();
});
$("#resultFilter").addEventListener("keydown", (event) => {
  if (event.key !== "Enter") return;
  event.preventDefault();
  navigateResultHighlight(event.shiftKey ? -1 : 1);
});
$("#clearResultFilter").addEventListener("click", () => {
  $("#resultFilter").value = "";
  activeResultHighlightIndex = -1;
  renderFilteredResults();
  $("#resultFilter").focus();
});
$("#settingsDialog").addEventListener("click", (event) => {
  if (event.target === $("#settingsDialog")) closeSettings();
});
$("#refresh").addEventListener("click", loadRequests);
$("#clearRequests").addEventListener("click", clearRequests);
$("#requestFilter").addEventListener("input", () => {
  updateFilterState();
  renderRequestList();
});
$("#saveFilter").addEventListener("click", openSaveFilter);
$("#filterHelp").addEventListener("click", () => {
  $("#filterHelpPopover").hidden = !$("#filterHelpPopover").hidden;
});
$("#closeSaveFilter").addEventListener("click", closeSaveFilter);
$("#cancelSaveFilter").addEventListener("click", closeSaveFilter);
$("#saveFilterForm").addEventListener("submit", saveFilterFavorite);
$("#saveFilterDialog").addEventListener("click", (event) => {
  if (event.target === $("#saveFilterDialog")) closeSaveFilter();
});
$("#saveKeyword").addEventListener("click", openSaveKeyword);
$("#closeSaveKeyword").addEventListener("click", closeSaveKeyword);
$("#cancelSaveKeyword").addEventListener("click", closeSaveKeyword);
$("#saveKeywordForm").addEventListener("submit", saveKeywordFavorite);
$("#saveKeywordDialog").addEventListener("click", (event) => {
  if (event.target === $("#saveKeywordDialog")) closeSaveKeyword();
});
document.addEventListener("click", (event) => {
  if (!event.target.closest("#filterHelp") && !event.target.closest("#filterHelpPopover")) {
    $("#filterHelpPopover").hidden = true;
  }
});
$("#search").addEventListener("click", runSearch);
chrome.devtools.network.onRequestFinished.addListener(scheduleRequestRefresh);

async function loadFilterFavorites() {
  const stored = await chrome.storage.local.get(SETTINGS_DEFAULTS);
  filterFavorites = Array.isArray(stored.requestFilterFavorites) ? stored.requestFilterFavorites : [];
  keywordFavorites = Array.isArray(stored.keywordFavorites) ? stored.keywordFavorites : [];
  renderFilterFavorites();
  renderKeywordFavorites();
}

async function openSettings() {
  await loadEnvironments();
  const config = currentEnvironment();
  renderEnvironmentSelectors();
  fillSettingsForm(config);
  $("#settingsStatus").textContent = "";
  $("#settingsStatus").classList.remove("danger");
  $("#settingsDialog").showModal();
}

function fillSettingsForm(config) {
  $("#settingsNodes").value = config.nodes.map((node) => node.name && node.name !== node.address ? `${node.name} ${node.address}` : node.address).join("\n");
  $("#settingsToken").value = config.token;
  $("#settingsTimeout").value = config.timeoutSeconds;
  $("#settingsConcurrency").value = config.concurrency;
  $("#settingsHeaders").value = config.requestIdHeaders.join(", ");
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
  const updated = {
    ...currentEnvironment(),
    nodes,
    token: $("#settingsToken").value.trim(),
    timeoutSeconds: Number($("#settingsTimeout").value) || 20,
    concurrency: Number($("#settingsConcurrency").value) || 5,
    requestIdHeaders: $("#settingsHeaders").value.split(",").map((item) => item.trim().toLowerCase()).filter(Boolean)
  };
  environments = environments.map((item) => item.id === activeEnvironmentId ? updated : item);
  await persistEnvironments();
  $("#settingsStatus").classList.remove("danger");
  $("#settingsStatus").textContent = "已保存";
  setTimeout(closeSettings, 350);
}

async function loadEnvironments(activateDefault = false) {
  const stored = await chrome.storage.local.get({ ...SETTINGS_DEFAULTS, ...ENVIRONMENT_DEFAULTS });
  environments = Array.isArray(stored.environments) ? stored.environments.filter((item) => item?.id && item?.name) : [];
  if (!environments.length) {
    environments = [{ id: newFavoriteId(), name: "默认环境", ...pickConnectionConfig(DEFAULT_ENVIRONMENT_CONFIG) }];
  }
  if (stored.environmentDefaultsVersion < CURRENT_ENVIRONMENT_DEFAULTS_VERSION) {
    environments = environments.map((environment) => environment.name === "默认环境"
      ? { id: environment.id, name: environment.name, ...pickConnectionConfig(DEFAULT_ENVIRONMENT_CONFIG) }
      : environment);
  }
  defaultEnvironmentId = environments.some((item) => item.id === stored.defaultEnvironmentId)
    ? stored.defaultEnvironmentId
    : environments[0].id;
  const preferredEnvironmentId = activateDefault ? defaultEnvironmentId : stored.activeEnvironmentId;
  activeEnvironmentId = environments.some((item) => item.id === preferredEnvironmentId) ? preferredEnvironmentId : defaultEnvironmentId;
  await persistEnvironments();
  renderEnvironmentSelectors();
}

function pickConnectionConfig(source = SETTINGS_DEFAULTS) {
  return {
    nodes: Array.isArray(source.nodes) ? source.nodes.map((node) => ({ ...node })) : [],
    token: String(source.token || ""),
    timeoutSeconds: Number(source.timeoutSeconds) || 20,
    concurrency: Number(source.concurrency) || 5,
    requestIdHeaders: Array.isArray(source.requestIdHeaders) ? [...source.requestIdHeaders] : [...SETTINGS_DEFAULTS.requestIdHeaders]
  };
}

function currentEnvironment() {
  return environments.find((item) => item.id === activeEnvironmentId) || environments[0];
}

function renderEnvironmentSelectors() {
  for (const selector of [$("#environmentSwitcher"), $("#settingsEnvironment")]) {
    selector.replaceChildren(...environments.map((environment) => {
      const option = document.createElement("option");
      option.value = environment.id;
      option.textContent = environment.id === defaultEnvironmentId ? `${environment.name}（默认）` : environment.name;
      option.selected = environment.id === activeEnvironmentId;
      return option;
    }));
  }
  $("#deleteEnvironment").disabled = environments.length < 2;
  const isDefault = activeEnvironmentId === defaultEnvironmentId;
  $("#setDefaultEnvironment").disabled = isDefault;
  $("#setDefaultEnvironment").textContent = isDefault ? "已是默认" : "设为默认";
}

async function persistEnvironments() {
  const active = currentEnvironment();
  await chrome.storage.local.set({
    environments,
    activeEnvironmentId,
    defaultEnvironmentId,
    environmentDefaultsVersion: CURRENT_ENVIRONMENT_DEFAULTS_VERSION,
    ...pickConnectionConfig(active)
  });
}

async function setDefaultEnvironment() {
  defaultEnvironmentId = activeEnvironmentId;
  await persistEnvironments();
  renderEnvironmentSelectors();
  $("#settingsStatus").classList.remove("danger");
  $("#settingsStatus").textContent = `已将“${currentEnvironment().name}”设为默认环境`;
}

async function switchEnvironment(id, fromSettings = false) {
  if (!environments.some((item) => item.id === id)) return;
  activeEnvironmentId = id;
  await persistEnvironments();
  renderEnvironmentSelectors();
  if (fromSettings || $("#settingsDialog").open) fillSettingsForm(currentEnvironment());
}

function openEnvironmentDialog(copyCurrent) {
  $("#environmentName").value = "";
  $("#copyEnvironment").checked = copyCurrent;
  $("#environmentStatus").textContent = "";
  $("#environmentStatus").classList.remove("danger");
  $("#environmentDialog").showModal();
  $("#environmentName").focus();
}

function closeEnvironmentDialog() {
  $("#environmentDialog").close();
}

async function createEnvironment(event) {
  event.preventDefault();
  const name = $("#environmentName").value.trim();
  if (!name) return showEnvironmentError("请输入环境名称");
  if (environments.some((item) => item.name.toLowerCase() === name.toLowerCase())) return showEnvironmentError("环境名称已存在");
  const base = $("#copyEnvironment").checked ? currentEnvironment() : SETTINGS_DEFAULTS;
  const environment = { id: newFavoriteId(), name, ...pickConnectionConfig(base) };
  environments.push(environment);
  activeEnvironmentId = environment.id;
  await persistEnvironments();
  renderEnvironmentSelectors();
  if ($("#settingsDialog").open) fillSettingsForm(environment);
  closeEnvironmentDialog();
}

function showEnvironmentError(message) {
  $("#environmentStatus").textContent = message;
  $("#environmentStatus").classList.add("danger");
}

async function deleteEnvironment() {
  if (environments.length < 2) return;
  const deletingDefault = activeEnvironmentId === defaultEnvironmentId;
  environments = environments.filter((item) => item.id !== activeEnvironmentId);
  activeEnvironmentId = environments[0].id;
  if (deletingDefault) defaultEnvironmentId = activeEnvironmentId;
  await persistEnvironments();
  renderEnvironmentSelectors();
  fillSettingsForm(currentEnvironment());
}

async function exportConfig() {
  const stored = await chrome.storage.local.get(null);
  const blob = new Blob([JSON.stringify(stored, null, 2)], { type: "application/json" });
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = `logsearch-config-${new Date().toISOString().slice(0, 10)}.json`;
  link.click();
  URL.revokeObjectURL(link.href);
}

async function importConfig(event) {
  const [file] = event.target.files;
  event.target.value = "";
  if (!file) return;
  try {
    const config = JSON.parse(await file.text());
    if (!config || typeof config !== "object" || Array.isArray(config)) throw new Error("配置格式错误");
    await chrome.storage.local.set(config);
    await loadEnvironments(true);
    fillSettingsForm(currentEnvironment());
    await loadFilterFavorites();
    $("#settingsStatus").textContent = "配置已导入";
    $("#settingsStatus").classList.remove("danger");
  } catch (error) {
    $("#settingsStatus").textContent = `导入失败：${error.message}`;
    $("#settingsStatus").classList.add("danger");
  }
}

function loadRequests() {
  chrome.devtools.network.getHAR((har) => {
    networkRequests = har.entries
      .filter((entry) => !requestClearTime || new Date(entry.startedDateTime).getTime() > requestClearTime)
      .slice(-200)
      .reverse();
    renderRequestList();
  });
}

function clearRequests() {
  requestClearTime = Date.now();
  networkRequests = [];
  selectedRequestKey = "";
  $("#selectedRequest").textContent = "请从左侧选择一个请求，或直接输入关键词";
  renderRequestList();
}

function scheduleRequestRefresh() {
  clearTimeout(requestRefreshTimer);
  requestRefreshTimer = setTimeout(loadRequests, 150);
}

function renderRequestList() {
  const filter = $("#requestFilter").value.trim();
  const list = $("#requestList");
  list.replaceChildren();
  let visible = 0;
  networkRequests.forEach((entry, index) => {
    const url = new URL(entry.request.url);
    const pathname = url.pathname || "/";
    const name = pathname.split("/").filter(Boolean).pop() || url.hostname;
    if (filter && !matchesRequestFilter(entry, filter)) return;
    const row = document.createElement("button");
    row.type = "button";
    row.className = "request-row";
    row.dataset.index = String(index);
    row.setAttribute("role", "option");
    const selected = requestKey(entry) === selectedRequestKey;
    row.classList.toggle("selected", selected);
    row.setAttribute("aria-selected", String(selected));
    row.title = entry.request.url;
    const nameCell = document.createElement("span");
    nameCell.className = "request-name";
    nameCell.textContent = name;
    const methodCell = document.createElement("span");
    methodCell.textContent = entry.request.method;
    const statusCell = document.createElement("span");
    statusCell.className = Number(entry.response.status) >= 400 ? "status-error" : "";
    statusCell.textContent = entry.response.status || "-";
    row.append(nameCell, methodCell, statusCell);
    row.addEventListener("click", () => selectRequest(index, row));
    list.append(row);
    visible++;
  });
  $("#requestCount").textContent = `${visible}/${networkRequests.length} 个请求`;
  if (!visible) {
    const empty = document.createElement("div");
    empty.className = "request-empty";
    empty.textContent = networkRequests.length ? "没有匹配的请求" : "暂无请求";
    list.append(empty);
  }
}

function requestKey(entry) {
  return `${entry.startedDateTime || ""}\n${entry.request.method || ""}\n${entry.request.url || ""}`;
}

const FILTER_FIELDS = new Set(["method", "status", "domain", "url", "path", "name", "type", "scheme", "header"]);

function matchesRequestFilter(entry, query) {
  const url = new URL(entry.request.url);
  const path = `${url.pathname || "/"}${url.search || ""}`;
  const name = (url.pathname || "/").split("/").filter(Boolean).pop() || url.hostname;
  const fields = {
    method: String(entry.request.method || ""),
    status: String(entry.response.status || ""),
    domain: url.hostname,
    url: entry.request.url,
    path,
    name,
    type: entry._resourceType || entry.response.content?.mimeType || "",
    scheme: url.protocol.replace(":", ""),
    header: [...(entry.request.headers || []), ...(entry.response.headers || [])].map((item) => `${item.name}:${item.value}`).join(" ")
  };
  const searchable = Object.values(fields).join(" ").toLowerCase();
  return tokenizeFilter(query).every((token) => {
    let expression = token;
    let excluded = false;
    if (expression.startsWith("-") && expression.length > 1) {
      excluded = true;
      expression = expression.slice(1);
    }
    const separator = expression.indexOf(":");
    const possibleField = separator > 0 ? expression.slice(0, separator).toLowerCase() : "";
    const field = FILTER_FIELDS.has(possibleField) ? possibleField : "";
    const value = unquote(field ? expression.slice(separator + 1) : expression);
    const matched = field
      ? matchesFilterField(field, fields[field], value)
      : searchable.includes(value.toLowerCase());
    return excluded ? !matched : matched;
  });
}

function tokenizeFilter(query) {
  return query.match(/(?:[^\s"]+|"[^"]*")+/g) || [];
}

function unquote(value) {
  const trimmed = value.trim();
  return trimmed.startsWith('"') && trimmed.endsWith('"') ? trimmed.slice(1, -1) : trimmed;
}

function matchesFilterField(field, source, value) {
  const actual = String(source || "").toLowerCase();
  const expected = value.toLowerCase();
  if (field === "status") {
    if (/^[1-5]xx$/.test(expected)) return actual.startsWith(expected[0]);
    const comparison = expected.match(/^(>=|<=|>|<)([0-9]{3})$/);
    if (comparison) {
      const status = Number(actual);
      const target = Number(comparison[2]);
      return { ">": status > target, ">=": status >= target, "<": status < target, "<=": status <= target }[comparison[1]];
    }
    return actual === expected;
  }
  if (field === "method" || field === "scheme") return actual === expected;
  return actual.includes(expected);
}

function updateFilterState() {
  const query = $("#requestFilter").value.trim();
  $("#saveFilter").classList.toggle("has-query", Boolean(query));
  renderFilterFavorites();
}

function renderFilterFavorites() {
  const container = $("#filterFavorites");
  const activeQuery = $("#requestFilter").value.trim();
  container.replaceChildren();
  if (!filterFavorites.length) {
    const empty = document.createElement("span");
    empty.className = "filter-favorites-empty";
    empty.textContent = "暂无收藏规则";
    container.append(empty);
    return;
  }
  for (const favorite of filterFavorites) {
    const item = document.createElement("div");
    item.className = "filter-favorite";
    item.classList.toggle("active", favorite.query === activeQuery);
    item.title = favorite.query;
    const apply = document.createElement("button");
    apply.type = "button";
    apply.className = "filter-favorite-label";
    apply.textContent = favorite.name;
    apply.addEventListener("click", () => {
      $("#requestFilter").value = favorite.query;
      updateFilterState();
      renderRequestList();
    });
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "filter-favorite-remove";
    remove.textContent = "×";
    remove.title = `删除“${favorite.name}”`;
    remove.setAttribute("aria-label", remove.title);
    remove.addEventListener("click", () => deleteFilterFavorite(favorite.id));
    item.append(apply, remove);
    container.append(item);
  }
}

function openSaveFilter() {
  const query = $("#requestFilter").value.trim();
  if (!query) {
    $("#requestFilter").focus();
    return;
  }
  $("#filterFavoriteName").value = query.length > 24 ? `${query.slice(0, 24)}…` : query;
  $("#filterFavoriteQuery").value = query;
  $("#filterFavoriteStatus").textContent = "";
  $("#filterFavoriteStatus").classList.remove("danger");
  $("#saveFilterDialog").showModal();
  $("#filterFavoriteName").select();
}

function closeSaveFilter() {
  $("#saveFilterDialog").close();
}

async function saveFilterFavorite(event) {
  event.preventDefault();
  const name = $("#filterFavoriteName").value.trim();
  const query = $("#filterFavoriteQuery").value.trim();
  if (!name || !query) {
    $("#filterFavoriteStatus").textContent = "名称和规则不能为空";
    $("#filterFavoriteStatus").classList.add("danger");
    return;
  }
  const duplicate = filterFavorites.find((item) => item.query === query);
  if (duplicate) {
    duplicate.name = name;
  } else {
    filterFavorites.push({ id: newFavoriteId(), name, query });
  }
  filterFavorites = filterFavorites.slice(-20);
  await chrome.storage.local.set({ requestFilterFavorites: filterFavorites });
  $("#filterFavoriteStatus").classList.remove("danger");
  renderFilterFavorites();
  closeSaveFilter();
}

async function deleteFilterFavorite(id) {
  filterFavorites = filterFavorites.filter((item) => item.id !== id);
  await chrome.storage.local.set({ requestFilterFavorites: filterFavorites });
  renderFilterFavorites();
}

function renderKeywordFavorites() {
  const container = $("#keywordFavorites");
  container.replaceChildren();
  if (!keywordFavorites.length) {
    const empty = document.createElement("span");
    empty.className = "keyword-favorites-empty";
    empty.textContent = "暂无收藏，输入关键词后点击“收藏关键词”";
    container.append(empty);
    return;
  }
  for (const favorite of keywordFavorites) {
    const item = document.createElement("div");
    item.className = "keyword-favorite";
    item.title = `追加关键词：${favorite.value}`;
    const append = document.createElement("button");
    append.type = "button";
    append.className = "keyword-favorite-append";
    append.textContent = favorite.name;
    append.addEventListener("click", () => appendFavoriteKeywords(favorite.value));
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "keyword-favorite-remove";
    remove.textContent = "×";
    remove.title = `删除“${favorite.name}”`;
    remove.setAttribute("aria-label", remove.title);
    remove.addEventListener("click", () => deleteKeywordFavorite(favorite.id));
    item.append(append, remove);
    container.append(item);
  }
}

function appendFavoriteKeywords(value) {
  const input = $("#keywords");
  const existing = splitKeywords(input.value);
  const seen = new Set(existing.map((item) => item.toLowerCase()));
  for (const keyword of splitKeywords(value)) {
    if (!seen.has(keyword.toLowerCase())) {
      existing.push(keyword);
      seen.add(keyword.toLowerCase());
    }
  }
  input.value = existing.join(", ");
  input.focus();
  input.setSelectionRange(input.value.length, input.value.length);
}

function openSaveKeyword() {
  const input = $("#keywords");
  const selected = input.value.slice(input.selectionStart || 0, input.selectionEnd || 0).trim();
  const values = splitKeywords(input.value);
  const suggested = selected || (values.length > 1 ? values[values.length - 1] : "");
  $("#keywordFavoriteName").value = suggested;
  $("#keywordFavoriteValue").value = suggested;
  $("#keywordFavoriteStatus").textContent = "";
  $("#keywordFavoriteStatus").classList.remove("danger");
  $("#saveKeywordDialog").showModal();
  (suggested ? $("#keywordFavoriteName") : $("#keywordFavoriteValue")).focus();
  if (suggested) $("#keywordFavoriteName").select();
}

function closeSaveKeyword() {
  $("#saveKeywordDialog").close();
}

async function saveKeywordFavorite(event) {
  event.preventDefault();
  const name = $("#keywordFavoriteName").value.trim();
  const value = splitKeywords($("#keywordFavoriteValue").value).join(", ");
  if (!name || !value) {
    $("#keywordFavoriteStatus").textContent = "名称和关键词不能为空";
    $("#keywordFavoriteStatus").classList.add("danger");
    return;
  }
  const duplicate = keywordFavorites.find((item) => item.value.toLowerCase() === value.toLowerCase());
  if (duplicate) {
    duplicate.name = name;
  } else {
    keywordFavorites.push({ id: newFavoriteId(), name, value });
  }
  keywordFavorites = keywordFavorites.slice(-30);
  await chrome.storage.local.set({ keywordFavorites });
  renderKeywordFavorites();
  closeSaveKeyword();
}

async function deleteKeywordFavorite(id) {
  keywordFavorites = keywordFavorites.filter((item) => item.id !== id);
  await chrome.storage.local.set({ keywordFavorites });
  renderKeywordFavorites();
}

function splitKeywords(value) {
  return value.split(/[,;\n]/).map((item) => item.trim()).filter(Boolean);
}

function newFavoriteId() {
  return crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

async function selectRequest(index, row) {
  const entry = networkRequests[Number(index)];
  selectedRequestKey = requestKey(entry);
  document.querySelectorAll(".request-row.selected").forEach((item) => {
    item.classList.remove("selected");
    item.setAttribute("aria-selected", "false");
  });
  row.classList.add("selected");
  row.setAttribute("aria-selected", "true");
  $("#selectedRequest").textContent = `${entry.request.method} ${entry.request.url} · ${entry.response.status}`;
  const config = await chrome.storage.local.get({ requestIdHeaders: ["x-request-id", "x-trace-id", "trace-id", "traceparent"] });
  const headers = [...entry.request.headers, ...entry.response.headers];
  for (const preferred of config.requestIdHeaders) {
    const header = headers.find((item) => item.name.toLowerCase() === preferred.toLowerCase());
    if (header?.value) {
      $("#keywords").value = header.value;
      return;
    }
  }
  const bodyRequestId = await requestIdFromResponseBody(entry);
  if (bodyRequestId) {
    $("#keywords").value = bodyRequestId;
    showSummary("已从响应 JSON 提取 Request ID");
    return;
  }
  $("#keywords").value = "";
  showSummary("该请求的请求头和响应头中没有找到 Request ID", true);
}

function requestIdFromResponseBody(entry) {
  return new Promise((resolve) => {
    entry.getContent((content) => {
      try {
        const body = JSON.parse(content);
        resolve(findRequestId(body));
      } catch {
        resolve("");
      }
    });
  });
}

function findRequestId(value, depth = 0) {
  if (!value || typeof value !== "object" || depth > 5) return "";
  for (const [key, child] of Object.entries(value)) {
    if (["requestid", "request_id", "traceid", "trace_id"].includes(key.toLowerCase()) && ["string", "number"].includes(typeof child)) {
      return String(child);
    }
  }
  for (const child of Object.values(value)) {
    const found = findRequestId(child, depth + 1);
    if (found) return found;
  }
  return "";
}

async function runSearch() {
  const keywords = splitKeywords($("#keywords").value);
  if (!keywords.length) return showSummary("请输入 Request ID 或关键词", true);
  $("#search").disabled = true;
  $("#results").replaceChildren();
  $("#errors").replaceChildren();
  $("#resultTools").hidden = true;
  $("#resultFilter").value = "";
  activeResultHighlightIndex = -1;
  renderedMatches = [];
  showSummary("正在查询所有节点…");
  const payload = {
    keywords,
    keywordMode: $("#mode").value,
    beforeContext: Number($("#before").value) || 0,
    afterContext: Number($("#after").value) || 0,
    maxResults: Number($("#maxResults").value) || 5000
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
  resultSummary = `${successful.length}/${nodes.length} 个节点成功，发现 ${discoveredFiles} 个文件，扫描 ${scannedFiles} 个，命中 ${matches.length} 条，耗时 ${elapsedMs} ms${truncatedText}`;
  showSummary(resultSummary);
  for (const node of failed) {
    const item = document.createElement("div");
    item.className = "error-item";
    item.textContent = `${node.node}: ${node.error}`;
    $("#errors").append(item);
  }
  renderedMatches = matches;
  $("#resultTools").hidden = false;
  renderFilteredResults();
}

function renderFilteredResults() {
  const terms = splitResultFilter($("#resultFilter").value);
  const visibleMatches = terms.length
    ? renderedMatches.filter((match) => {
      const searchable = matchSearchableText(match).toLowerCase();
      return terms.every((term) => searchable.includes(term.toLowerCase()));
    })
    : renderedMatches;
  const results = $("#results");
  results.replaceChildren(...visibleMatches.map((match) => renderMatch(match, terms)));
  $("#clearResultFilter").hidden = !terms.length;
  const highlightCount = results.querySelectorAll("mark").length;
  $("#resultFilterCount").textContent = terms.length
    ? `${visibleMatches.length}/${renderedMatches.length} 条 · ${highlightCount} 个命中`
    : `${renderedMatches.length} 条`;
  showSummary(terms.length ? `${resultSummary}；前端过滤后显示 ${visibleMatches.length} 条` : resultSummary);
  if (!visibleMatches.length) {
    const empty = document.createElement("div");
    empty.className = "card empty";
    empty.textContent = terms.length ? "返回结果中没有匹配项" : "没有找到匹配日志";
    results.append(empty);
  }
}

function navigateResultHighlight(direction) {
  const highlights = [...$("#results").querySelectorAll("mark")];
  if (!highlights.length) return;
  highlights.forEach((mark) => {
    mark.classList.remove("active-result-highlight");
    mark.removeAttribute("aria-current");
  });
  activeResultHighlightIndex = activeResultHighlightIndex < 0
    ? (direction > 0 ? 0 : highlights.length - 1)
    : (activeResultHighlightIndex + direction + highlights.length) % highlights.length;
  const active = highlights[activeResultHighlightIndex];
  active.classList.add("active-result-highlight");
  active.setAttribute("aria-current", "true");
  active.scrollIntoView({ behavior: "smooth", block: "center", inline: "nearest" });
  $("#resultFilterCount").textContent = `${activeResultHighlightIndex + 1}/${highlights.length} 个命中`;
}

function splitResultFilter(value) {
  return [...new Set(value.split(/[\s,;]+/).map((item) => item.trim()).filter(Boolean))];
}

function matchSearchableText(match) {
  return [
    match.sourceNode,
    match.sourceType,
    match.namespace,
    match.pod,
    match.container,
    match.file,
    match.lineNumber,
    ...(match.before || []),
    match.text,
    ...(match.after || [])
  ].filter((value) => value !== undefined && value !== null).join("\n");
}

function renderMatch(match, highlightTerms = []) {
  const article = document.createElement("article");
  article.className = "card log-entry";
  const meta = document.createElement("div");
  meta.className = "log-meta";
  const metaText = `${match.sourceNode} · ${match.sourceType || "kubelet"} · ${match.namespace || "-"}/${match.pod || "-"}/${match.container || "-"} · ${match.file || "-"} · line ${match.lineNumber}`;
  appendHighlightedText(meta, metaText, highlightTerms);
  const pre = document.createElement("pre");
  const lines = [...(match.before || []), match.text, ...(match.after || [])];
  const logText = lines.join("\n");
  appendHighlightedText(pre, logText, highlightTerms);
  const copy = document.createElement("button");
  copy.className = "copy";
  copy.textContent = "复制";
  copy.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(logText);
      showCopyStatus(copy, "✓ 已复制", false);
    } catch {
      showCopyStatus(copy, "复制失败", true);
    }
  });
  article.append(meta, copy, pre);
  return article;
}

function appendHighlightedText(container, text, terms) {
  if (!terms.length) {
    container.textContent = text;
    return;
  }
  const escaped = terms.sort((a, b) => b.length - a.length).map((term) => term.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));
  const matcher = new RegExp(`(${escaped.join("|")})`, "gi");
  let lastIndex = 0;
  for (const match of text.matchAll(matcher)) {
    container.append(document.createTextNode(text.slice(lastIndex, match.index)));
    const mark = document.createElement("mark");
    mark.textContent = match[0];
    container.append(mark);
    lastIndex = match.index + match[0].length;
  }
  container.append(document.createTextNode(text.slice(lastIndex)));
}

function showCopyStatus(button, message, failed) {
  clearTimeout(button.copyStatusTimer);
  button.textContent = message;
  button.classList.toggle("copy-failed", failed);
  button.classList.toggle("copy-success", !failed);
  button.copyStatusTimer = setTimeout(() => {
    button.textContent = "复制";
    button.classList.remove("copy-failed", "copy-success");
  }, 1400);
}

function showSummary(message, error = false) {
  $("#summary").textContent = message;
  $("#summary").classList.toggle("danger", error);
}

loadEnvironments(true);
loadFilterFavorites();
loadRequests();
