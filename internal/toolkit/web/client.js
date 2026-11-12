export const base = new URL("../", import.meta.url).pathname.replace(/\/$/, "");
const styleURL = new URL("./client.css", import.meta.url).pathname;
function ensureStyles() {
  if (!document.querySelector("link[data-haven-tools]")) {
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = styleURL;
    link.dataset.havenTools = "";
    document.head.append(link);
  }
}
export function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text !== undefined && text !== null) node.textContent = text;
  if (className) node.className = className;
  return node;
}
export function icon(value) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  for (const [key, text] of Object.entries({
    viewBox: "0 0 24 24",
    fill: "none",
    stroke: "currentColor",
    "stroke-width": "1.7",
    "stroke-linecap": "round",
    "stroke-linejoin": "round",
    "aria-hidden": "true",
  }))
    svg.setAttribute(key, text);
  svg.classList.add("ht-icon");
  const paths = Array.isArray(value?.paths) ? value.paths.slice(0, 8) : [];
  for (const d of paths) {
    if (
      typeof d !== "string" ||
      d.length > 4096 ||
      !/^[MmLlHhVvCcSsQqTtAaZz0-9eE.,+\s-]+$/.test(d)
    )
      continue;
    const path = document.createElementNS(svg.namespaceURI, "path");
    path.setAttribute("d", d);
    svg.append(path);
  }
  return svg;
}

export async function api(
  path,
  { body, signal, method, headers = {}, timeout = 20000 } = {},
) {
  if (globalThis.__HAVEN_STATIC__) {
    const tools = globalThis.__HAVEN_STATIC__.tools;
    if (path === "/v1/catalog") return {tools};
    if (path === "/v1/suggest") {
      const {text = ""} = JSON.parse(body || "{}");
      return {actions: tools.filter(t => !text.trim() || [t.name,t.title,t.description,t.example].join(" ").toLowerCase().includes(text.toLowerCase())).map(t => ({id:t.name,kind:"remote",handlerId:t.name,editor:true,arguments:{text},title:t.title,detail:t.description,icon:t.icon,mode:t.mode,inputKind:t.inputKind}))};
    }
    throw Error("此功能需要自托管服务，浏览器版不发送文件或后台请求。");
  }
  const own = new AbortController();
  let timedOut = false;
  const abort = () => own.abort();
  signal?.addEventListener("abort", abort, { once: true });
  if (signal?.aborted) abort();
  const timer =
    timeout > 0
      ? setTimeout(() => {
          timedOut = true;
          own.abort();
        }, timeout)
      : undefined;
  try {
    const response = await fetch(base + path, {
      method: method || (body === undefined ? "GET" : "POST"),
      credentials: "same-origin",
      signal: own.signal,
      headers: {
        ...(body !== undefined && typeof body === "string"
          ? { "Content-Type": "application/json" }
          : {}),
        ...headers,
      },
      body,
    });
    if (response.status === 413)
      throw Error(
        "上传被入口代理或上游服务的单次请求大小限制拒绝；可使用内网入口或公开文件直链。",
      );
    if (
      !/^application\/json(?:;|$)/i.test(
        response.headers.get("content-type") || "",
      )
    )
      throw Error("工具服务暂不可用或登录已过期，请刷新后重试。");
    const data = await response.json();
    if (!response.ok) throw Error(data.error || "请求失败，请重试。");
    if (data.protocolVersion !== 1) throw Error("工具版本已更新，请刷新页面。");
    return data;
  } catch (error) {
    if (timedOut) throw Error("请求超时，请重试。");
    throw error;
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", abort);
  }
}
export async function suggest(text, inputKind = "text", signal) {
  const data = await api("/v1/suggest", {
    body: JSON.stringify({
      protocolVersion: 1,
      text,
      inputKind,
      resolver: "rules",
    }),
    signal,
  });
  if (!Array.isArray(data.actions) || data.actions.length > 40)
    throw Error("工具建议格式无效。");
  return data.actions;
}

export async function execute(
  action,
  file,
  signal,
  requestId = crypto.randomUUID(),
) {
  if (action.editor)
    return { result: { type: "editor", action, file, signal } };
  if (["image", "file"].includes(action.inputKind)) {
    if (!/^\/v1\/(?:image|files\/[a-z0-9-]+)$/.test(action.uploadPath || ""))
      throw Error("文件操作入口无效。");
    if (action.uploadPath !== "/v1/image") {
      if (!file) throw Error("请先选择一个文件。");
      return api(action.uploadPath, {
        body: file,
        signal,
        headers: {
          "Content-Type": file.type || "application/octet-stream",
          "X-Request-ID": requestId,
        },
        timeout: 0,
      });
    }
    if (!file) throw Error("请先选择一张图片。");
    let image = file;
    // AVIF transport compatibility only; QR recognition remains on the server.
    if (file.type === "image/avif") {
      const bitmap = await createImageBitmap(file);
      try {
        if (bitmap.width * bitmap.height > 32000000)
          throw Error("图片超过 3200 万像素。");
        const canvas = new OffscreenCanvas(bitmap.width, bitmap.height);
        canvas.getContext("2d").drawImage(bitmap, 0, 0);
        image = await canvas.convertToBlob({ type: "image/png" });
      } finally {
        bitmap.close();
      }
    }
    if (signal?.aborted) throw new DOMException("Cancelled", "AbortError");
    return api("/v1/image", {
      body: image,
      signal,
      headers: { "Content-Type": image.type || "application/octet-stream" },
      timeout: 0,
    });
  }
  return api("/v1/execute", {
    body: JSON.stringify({
      protocolVersion: 1,
      name: action.handlerId,
      arguments: action.arguments,
      requestId,
    }),
    signal,
  });
}

function safeURL(value) {
  try {
    const url = new URL(value, location.href);
    return ["http:", "https:"].includes(url.protocol) ? url.href : null;
  } catch {
    return null;
  }
}
export function showError(parent, error, retry) {
  parent.replaceChildren(element("p", error.message, "ht-error"));
  if (retry) {
    const button = element("button", "重试");
    button.onclick = retry;
    parent.append(button);
  }
}
async function copy(text, button) {
  try {
    await navigator.clipboard.writeText(text);
    button.textContent = "已复制";
  } catch {
    button.textContent = "复制失败，请手动选择内容";
  }
}
export function renderResult(parent, result, { onBack, onHandoff } = {}) {
  if (result?.type === "editor") {
    ensureStyles();
    parent.classList.add("ht-view");
    const marker = element("p", "正在打开工具…");
    parent.replaceChildren(marker);
    import("./editor.js")
      .then(({ renderEditor }) => {
        if (marker.isConnected && !result.signal?.aborted)
          renderEditor(parent, result, { onBack, onHandoff });
      })
      .catch((error) => {
        if (marker.isConnected) showError(parent, error, onBack);
      });
    return;
  }
  if (
    !result ||
    !["text", "json", "fields"].includes(result.type) ||
    typeof result.text !== "string" ||
    typeof result.title !== "string"
  )
    throw Error("工具结果格式无效。");
  ensureStyles();
  parent.classList.add("ht-view");
  parent.replaceChildren();
  const panel = element("section", null, "ht-result"),
    head = element("div", null, "ht-head");
  head.append(icon(result.icon), element("h2", result.title));
  panel.append(head);
  if (result.type === "fields" || result.fields?.length) {
    if (!Array.isArray(result.fields) || result.fields.length > 30)
      throw Error("字段结果格式无效。");
    const dl = element("dl");
    for (const field of result.fields) {
      if (typeof field.label !== "string" || typeof field.value !== "string")
        throw Error("字段格式无效。");
      dl.append(element("dt", field.label), element("dd", field.value));
    }
    panel.append(dl);
  }
  if (result.description)
    panel.append(element("p", result.description, "ht-description"));
  if (result.type !== "fields") {
    const pre = element("pre", result.text);
    if (result.type === "json") pre.dataset.format = "json";
    panel.append(pre);
  }
  const actions = element("div", null, "ht-controls"),
    copyButton = element("button", "复制内容");
  copyButton.onclick = () => copy(result.text, copyButton);
  actions.append(copyButton);
  for (const link of result.links || []) {
    const href = safeURL(link.href);
    if (!href) continue;
    const anchor = element("a", link.label, "ht-button");
    anchor.href = href;
    anchor.target = "_blank";
    anchor.rel = "noopener noreferrer";
    actions.append(anchor);
  }
  if (onHandoff) {
    const button = element("button", "带着结果继续分析");
    button.onclick = () => onHandoff(result.text);
    actions.append(button);
  }
  if (onBack) {
    const button = element("button", "返回操作");
    button.onclick = onBack;
    actions.append(button);
  }
  panel.append(actions);
  parent.append(panel);
  if (result.copyOnSelect) copy(result.text, copyButton);
}

const states = {
  queued: "等待中",
  running: "处理中",
  succeeded: "已完成",
  failed: "失败",
  canceled: "已取消",
  interrupted: "已中断",
};
export async function taskView(parent, id, { onBack, onHandoff, signal } = {}) {
  ensureStyles();
  parent.classList.add("ht-view");
  let timer,
    generation = 0,
    lastVersion;
  if (globalThis.__HAVEN_STATIC__) {
    const tools = globalThis.__HAVEN_STATIC__.tools;
    if (path === "/v1/catalog") return {tools};
    if (path === "/v1/suggest") {
      const {text = ""} = JSON.parse(body || "{}");
      return {actions: tools.filter(t => !text.trim() || [t.name,t.title,t.description,t.example].join(" ").toLowerCase().includes(text.toLowerCase())).map(t => ({id:t.name,kind:"remote",handlerId:t.name,editor:true,arguments:{text},title:t.title,detail:t.description,icon:t.icon,mode:t.mode,inputKind:t.inputKind}))};
    }
    throw Error("此功能需要自托管服务，浏览器版不发送文件或后台请求。");
  }
  const own = new AbortController();
  const cancel = () => {
    clearTimeout(timer);
    own.abort();
  };
  signal?.addEventListener("abort", cancel, { once: true });
  if (signal?.aborted) cancel();
  async function refresh() {
    clearTimeout(timer);
    const ownGeneration = ++generation;
    if (own.signal.aborted || !parent.isConnected) {
      cancel();
      return;
    }
    try {
      const { task } = await api("/v1/tasks/" + encodeURIComponent(id), {
        signal: own.signal,
      });
      if (
        own.signal.aborted ||
        !parent.isConnected ||
        ownGeneration !== generation
      )
        return;
      if (
        lastVersion === task.updatedAt &&
        ["queued", "running"].includes(task.state)
      ) {
        timer = setTimeout(refresh, 1500);
        return;
      }
      lastVersion = task.updatedAt;
      parent.replaceChildren();
      const head = element("div", null, "ht-task-head");
      head.append(element("h1", task.title));
      const state = element(
        "span",
        states[task.state] || task.state,
        "ht-status",
      );
      state.dataset.state = task.state;
      head.append(state);
      parent.append(head);
      const meta = element(
        "p",
        "提交于 " +
          new Date(task.createdAt).toLocaleString("zh-CN", {
            timeZone: "Asia/Shanghai",
            hour12: false,
          }),
        "ht-subtle",
      );
      parent.append(meta);
      if (task.stage && ["queued", "running"].includes(task.state))
        parent.append(element("p", task.stage, "ht-phase"));
      if (task.result) {
        const result = element("div");
        parent.append(result);
        const visible = { ...task.result };
        if (task.filesExpireAt) {
          const available =
            task.filesAvailable === true &&
            Date.now() < Date.parse(task.filesExpireAt);
          parent.append(
            element(
              "p",
              available
                ? "临时文件有效至 " +
                    new Date(task.filesExpireAt).toLocaleString("zh-CN", {
                      timeZone: "Asia/Shanghai",
                      hour12: false,
                    })
                : "临时文件已过期或被服务重建清理，文字结果仍保留。",
              "ht-subtle",
            ),
          );
          if (!available)
            visible.links = (visible.links || []).filter(
              (link) =>
                !new URL(link.href, location.href).pathname.startsWith(
                  base + "/tasks/" + id + "/files/",
                ),
            );
        }
        renderResult(result, visible, { onHandoff });
      } else if (task.error)
        parent.append(element("p", task.error, "ht-error"));
      else
        parent.append(
          element(
            "div",
            "任务在后台执行，关闭页面后仍可回来查看。",
            "ht-empty",
          ),
        );
      if (task.notification) {
        const messages = {
          sending: "正在发送 ntfy 完成通知…",
          sent: "已发送 ntfy 通知 · 结果保存在本页",
          failed: "ntfy 通知失败，结果已保存。请检查通知服务。",
          unknown: "通知发送状态未确认，结果已保存。请核对通知历史。",
        };
        parent.append(
          element("p", messages[task.notification.state] || "", "ht-subtle"),
        );
        if (task.notification.state === "sending")
          timer = setTimeout(refresh, 1500);
      }
      const controls = element("div", null, "ht-controls"),
        link = element("a", "打开结果页", "ht-button");
      link.href = safeURL(task.href) || base + "/tasks";
      link.target = "_blank";
      link.rel = "noopener noreferrer";
      controls.append(link);
      const copyLink = element("button", "复制结果链接");
      copyLink.onclick = () =>
        copy(new URL(task.href, location.href).href, copyLink);
      controls.append(copyLink);
      const history = element("a", "任务列表", "ht-button");
      history.href = base + "/tasks";
      history.target = "_blank";
      history.rel = "noopener noreferrer";
      controls.append(history);
      if (["queued", "running"].includes(task.state)) {
        const button = element("button", "取消任务");
        button.onclick = async () => {
          button.disabled = true;
          try {
            await api("/v1/tasks/" + id + "/cancel", {
              method: "POST",
              signal: own.signal,
            });
            clearTimeout(timer);
            refresh();
          } catch (error) {
            button.disabled = false;
            showError(parent, error, refresh);
          }
        };
        controls.append(button);
        timer = setTimeout(refresh, 1500);
      } else if (task.notification?.state !== "sending")
        signal?.removeEventListener("abort", cancel);
      if (onBack) {
        const button = element("button", "返回操作");
        button.onclick = () => {
          cancel();
          onBack();
        };
        controls.append(button);
      }
      parent.append(controls);
    } catch (error) {
      if (error.name !== "AbortError") showError(parent, error, refresh);
    }
  }
  await refresh();
  return cancel;
}
