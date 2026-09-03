const DEFAULT_CONFIG = {
  nodes: [],
  token: "",
  timeoutSeconds: 20,
  concurrency: 5,
  requestIdHeaders: ["x-request-id", "x-trace-id", "trace-id", "traceparent"]
};

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  if (message.type !== "search") return false;
  searchAllNodes(message.payload)
    .then((data) => sendResponse({ ok: true, data }))
    .catch((error) => sendResponse({ ok: false, error: String(error.message || error) }));
  return true;
});

async function searchAllNodes(query) {
  const stored = await chrome.storage.local.get(DEFAULT_CONFIG);
  const nodes = stored.nodes.filter((node) => node.enabled !== false && node.address);
  if (!nodes.length) throw new Error("请先在扩展设置中配置至少一个节点");
  const concurrency = Math.max(1, Math.min(Number(stored.concurrency) || 5, 20));
  const results = new Array(nodes.length);
  let cursor = 0;

  async function worker() {
    while (cursor < nodes.length) {
      const index = cursor++;
      results[index] = await searchNode(nodes[index], query, stored);
    }
  }
  await Promise.all(Array.from({ length: Math.min(concurrency, nodes.length) }, worker));
  return results;
}

async function searchNode(node, query, config) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), (Number(config.timeoutSeconds) || 20) * 1000);
  const started = performance.now();
  const headers = {
    "Content-Type": "application/json",
    "Connect-Protocol-Version": "1"
  };
  if (config.token) headers.Authorization = `Bearer ${config.token}`;
  try {
    const address = node.address.replace(/\/$/, "");
    const response = await fetch(`${address}/logsearch.v1.LogSearchService/Search`, {
      method: "POST",
      headers,
      body: JSON.stringify(query),
      signal: controller.signal
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.message || `${response.status} ${response.statusText}`);
    return { node: node.name || node.address, address: node.address, ok: true, elapsedMs: Math.round(performance.now() - started), data: body };
  } catch (error) {
    const message = error.name === "AbortError" ? "查询超时" : String(error.message || error);
    return { node: node.name || node.address, address: node.address, ok: false, elapsedMs: Math.round(performance.now() - started), error: message };
  } finally {
    clearTimeout(timeout);
  }
}
