const defaults = { nodes: [], token: "", timeoutSeconds: 20, concurrency: 5, requestIdHeaders: ["x-request-id", "x-trace-id", "trace-id", "traceparent"] };

async function load() {
  const config = await chrome.storage.local.get(defaults);
  document.querySelector("#nodes").value = config.nodes.map((node) => `${node.name || "node"} ${node.address}`).join("\n");
  document.querySelector("#token").value = config.token;
  document.querySelector("#timeout").value = config.timeoutSeconds;
  document.querySelector("#concurrency").value = config.concurrency;
  document.querySelector("#headers").value = config.requestIdHeaders.join(", ");
}

document.querySelector("#save").addEventListener("click", async () => {
  const nodes = document.querySelector("#nodes").value.split("\n").map((line) => line.trim()).filter(Boolean).map((line) => {
    const separator = line.search(/\s+/);
    return separator < 0 ? { name: line, address: line, enabled: true } : { name: line.slice(0, separator), address: line.slice(separator).trim(), enabled: true };
  });
  await chrome.storage.local.set({
    nodes,
    token: document.querySelector("#token").value.trim(),
    timeoutSeconds: Number(document.querySelector("#timeout").value) || 20,
    concurrency: Number(document.querySelector("#concurrency").value) || 5,
    requestIdHeaders: document.querySelector("#headers").value.split(",").map((item) => item.trim().toLowerCase()).filter(Boolean)
  });
  const status = document.querySelector("#status");
  status.textContent = "已保存";
  setTimeout(() => { status.textContent = ""; }, 1500);
});

load();
