import {
  api,
  base,
  renderResult,
  icon,
  element,
  execute,
  suggest,
  taskView,
  showError,
} from "./client.js";

const workspace = document.getElementById("workspace");
let revision = 0,
  controller,
  timer,
  actions = [],
  selected = 0,
  file,
  pending = Promise.resolve(),
  release = () => {};
const stateNames = {
  queued: "等待中",
  running: "处理中",
  succeeded: "已完成",
  failed: "失败",
  canceled: "已取消",
  interrupted: "已中断",
};
const taskMatch = location.pathname.match(/\/tasks\/([a-f0-9-]+)$/);
if (taskMatch) taskView(workspace, taskMatch[1]);
else if (location.pathname.replace(/\/$/, "").endsWith("/tasks")) history();
else palette();

async function history() {
  workspace.replaceChildren(
    element("h1", "后台任务"),
    element("p", "查看进度，或回来领取已经完成的结果。"),
  );
  const list = element("div", null, "list");
  const refresh = element("button", "刷新任务");
  refresh.onclick = history;
  workspace.append(refresh, list);
  try {
    const data = await api("/v1/tasks");
    if (!data.tasks.length) {
      list.append(element("div", "还没有后台任务。", "empty"));
      return;
    }
    for (const task of data.tasks) {
      const link = element("a", null, "task");
      link.href = task.href;
      const body = element("div", null, "option-text");
      body.append(
        element("div", task.title, "title"),
        element(
          "div",
          new Date(task.createdAt).toLocaleString("zh-CN", {
            timeZone: "Asia/Shanghai",
            hour12: false,
          }),
          "detail",
        ),
      );
      const state = element(
        "span",
        stateNames[task.state] || task.state,
        "status",
      );
      state.dataset.state = task.state;
      link.append(
        icon({
          name: "tasks",
          paths: ["m3 6 2 2 3-4M11 6h10m-18 8 2 2 3-4M11 14h10M11 20h10"],
        }),
        body,
        state,
      );
      list.append(link);
    }
  } catch (error) {
    showError(list, error, history);
  }
}

function palette() {
  workspace.replaceChildren(
    element("h1", "工具台"),
    element("p", "选择一个工具，或放入内容，找到合适的操作。"),
  );
  const form = element("form", null, "input"),
    input = element("textarea");
  input.rows = 2;
  let browsing = true;
  input.placeholder = "输入算式、文本或分享链接，也可以拖入文件";
  input.setAttribute("aria-label", "工具输入");
  input.setAttribute("role", "combobox");
  input.setAttribute("aria-autocomplete", "list");
  input.setAttribute("aria-controls", "tool-options");
  const picker = element("input");
  picker.type = "file";
  picker.hidden = true;
  const attach = element("button", null, "attach-button");
  attach.setAttribute("aria-label", "添加附件");
  attach.title = "添加附件";
  attach.append(
    icon({
      paths: [
        "m21 11-8.5 8.5a6 6 0 0 1-8.5-8.5l9-9a4 4 0 0 1 5.7 5.7l-9 9a2 2 0 0 1-2.8-2.8l8.5-8.5",
      ],
    }),
  );
  attach.type = "button";
  attach.onclick = () => picker.click();
  form.append(input, attach, picker);
  workspace.append(form);
  const attachment = element("div", null, "file"),
    list = element("div", null, "list");
  list.id = "tool-options";
  list.setAttribute("role", "listbox");
  const toolbar = element("div", null, "tools-toolbar");
  const heading = element("h2", "全部工具", "tools-heading");
  const browse = element("button", "全部工具", "browse-button");
  browse.type = "button";
  browse.prepend(
    icon({ paths: ["M3 3h7v7H3zM14 3h7v7h-7zM3 14h7v7H3zM14 14h7v7h-7z"] }),
  );
  browse.onclick = () => {
    browsing = true;
    refresh();
  };
  toolbar.append(heading, browse);
  workspace.append(attachment, toolbar, list);
  function clear() {
    controller?.abort();
    clearTimeout(timer);
    release();
    revision++;
  }
  function refresh() {
    clear();
    actions = [];
    list
      .querySelectorAll("button")
      .forEach((button) => (button.disabled = true));
    const own = revision;
    controller = new AbortController();
    const signal = controller.signal;
    pending = new Promise((resolve) => {
      release = resolve;
      timer = setTimeout(async () => {
        try {
          const data = browsing
            ? (await api("/v1/catalog", { signal })).tools.map((tool) => ({
                id: "directory/" + tool.name,
                kind: tool.editor
                  ? "remote"
                  : tool.inputKind === "image"
                    ? "pick-image"
                    : "fill",
                handlerId: tool.name,
                editor: tool.editor,
                arguments: { text: "" },
                inputKind: tool.inputKind,
                title: tool.title,
                detail: tool.description,
                icon: tool.icon,
                value: tool.example,
                category: tool.category,
                mode: tool.mode,
              }))
            : await suggest(
                input.value,
                file
                  ? file.type.startsWith("image/")
                    ? "image"
                    : "file"
                  : "text",
                signal,
              );
          if (own !== revision) return;
          actions = data;
          selected = 0;
          draw();
        } catch (error) {
          if (own === revision && error.name !== "AbortError")
            showError(list, error, refresh);
        } finally {
          resolve();
        }
      }, 75);
    });
  }
  function draw() {
    list.replaceChildren();
    list.classList.toggle("tool-grid", browsing);
    heading.textContent = browsing
      ? "全部工具 · " + actions.length
      : "匹配操作";
    input.setAttribute("aria-expanded", String(actions.length > 0));
    actions.forEach((action, index) => {
      const button = element("button", null, "option");
      button.type = "button";
      button.setAttribute("role", "option");
      button.setAttribute("aria-selected", String(index === selected));
      button.id = "tool-option-" + index;
      const text = element("span", null, "option-text");
      text.append(
        element("span", action.title, "title"),
        element("div", action.detail, "detail"),
      );
      if (browsing)
        text.prepend(
          element(
            "span",
            action.category +
              " / " +
              (action.mode === "browser"
                ? "本机处理"
                : action.mode === "async"
                  ? "后台任务"
                  : "即时工具"),
            "tool-meta",
          ),
        );
      button.append(icon(action.icon), text);
      button.onclick = () => run(action);
      list.append(button);
    });
    if (actions.length)
      input.setAttribute("aria-activedescendant", "tool-option-" + selected);
    else input.removeAttribute("aria-activedescendant");
    if (!actions.length)
      list.append(
        element(
          "div",
          input.value
            ? "没有匹配的操作，可以点击“全部工具”选择。"
            : "暂时没有可用工具。",
          "empty",
        ),
      );
  }
  async function run(action, requestId = crypto.randomUUID()) {
    if (!actions.includes(action)) return;
    if (action.kind === "fill") {
      input.value = action.value;
      browsing = false;
      file = null;
      attachment.replaceChildren();
      refresh();
      input.focus();
      return;
    }
    if (action.kind === "pick-image") {
      picker.click();
      return;
    }
    clear();
    const own = revision;
    controller = new AbortController();
    list.classList.remove("tool-grid");
    heading.textContent = action.title;
    input.setAttribute("aria-expanded", "false");
    input.removeAttribute("aria-activedescendant");
    list.replaceChildren(element("p", "正在处理…"));
    try {
      const data = await execute(action, file, controller.signal, requestId);
      if (own !== revision) return;
      if (data.task) {
        list.replaceChildren();
        taskView(list, data.task.id, {
          onBack: refresh,
          signal: controller.signal,
        });
      } else renderResult(list, data.result, { onBack: refresh });
    } catch (error) {
      if (own === revision && error.name !== "AbortError")
        showError(list, error, () => run(action, requestId));
    }
  }
  function attachFile(next) {
    if (!next) return;
    file = next;
    browsing = false;
    attachment.replaceChildren(element("span", file.name + " "));
    const remove = element("button", "移除");
    remove.onclick = () => {
      file = null;
      attachment.replaceChildren();
      picker.value = "";
      browsing = !input.value.trim();
      refresh();
    };
    attachment.append(remove);
    refresh();
  }
  input.addEventListener("input", () => {
    browsing = !input.value.trim() && !file;
    refresh();
  });
  picker.onchange = () => attachFile(picker.files[0]);
  form.onsubmit = (event) => event.preventDefault();
  input.onkeydown = async (event) => {
    if (event.isComposing) return;
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      const own = revision;
      await pending;
      if (own === revision && actions[selected]) run(actions[selected]);
    } else if (["ArrowDown", "ArrowUp"].includes(event.key) && actions.length) {
      event.preventDefault();
      selected =
        (selected + (event.key === "ArrowDown" ? 1 : -1) + actions.length) %
        actions.length;
      draw();
    }
  };
  form.addEventListener("paste", (event) => {
    const image = event.clipboardData?.files?.[0];
    if (image) {
      event.preventDefault();
      attachFile(image);
    }
  });
  form.ondragover = (event) => {
    if (event.dataTransfer.types.includes("Files")) event.preventDefault();
  };
  form.ondrop = (event) => {
    event.preventDefault();
    attachFile(event.dataTransfer.files[0]);
  };
  input.focus();
  refresh();
  addEventListener("pagehide", clear, { once: true });
}
