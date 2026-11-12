import { element, api, taskView } from "./client.js";
const supported = new Set([
  "pdf-workbench",
  "images-pdf",
  "config-diff",
  "cron-preview",
  "media-info",
  "image-metadata",
  "qr-generate",
  "media-clip",
]);
export function renderEditor(
  parent,
  { action, file, signal },
  { onBack, onHandoff } = {},
) {
  const name = action.handlerId;
  if (!supported.has(name)) throw Error("不支持的工具界面，请刷新页面。");
  const panel = element("section", null, "ht-result ht-editor"),
    form = element("form"),
    out = element("div");
  panel.append(
    element("h2", action.title),
    element(
      "p",
      name === "media-clip"
        ? "截取需要上传完整源文件或提供公开直链；大文件优先使用内网入口。快速模式按关键帧截取，精确模式重新编码。"
        : "在本机浏览器处理，文件与文本不会上传；关闭界面会释放文件和结果。",
      "ht-subtle",
    ),
    form,
    out,
  );
  parent.replaceChildren(panel);
  let files = file ? [file] : [],
    worker,
    urls = [],
    disposed = false,
    submitted;
  const fields = {};
  const releaseURLs = () => {
    for (const u of urls) URL.revokeObjectURL(u);
    urls = [];
  };
  const cleanup = () => {
    disposed = true;
    worker?.terminate();
    releaseURLs();
    observer.disconnect();
    signal?.removeEventListener("abort", cleanup);
  };
  const observer = new MutationObserver(() => {
    if (!panel.isConnected) cleanup();
  });
  observer.observe(parent, { childList: true });
  signal?.addEventListener("abort", cleanup, { once: true });
  function field(key, label, kind = "input", value = "") {
    const wrap = element("label", null, "ht-field"),
      control = element(kind);
    wrap.append(element("span", label), control);
    form.append(wrap);
    fields[key] = control;
    if (kind === "textarea") {
      control.rows = 7;
      control.spellcheck = false;
    } else if (kind === "input") control.type = "text";
    control.value = value;
    return control;
  }
  function select(key, label, choices) {
    const control = field(key, label, "select");
    for (const [value, text] of choices) {
      const option = element("option", text);
      option.value = value;
      control.append(option);
    }
    return control;
  }
  const fileNames = element("div", null, "ht-file-list");
  function drawFiles() {
    fileNames.replaceChildren();
    files.forEach((f, i) => {
      const row = element("div", null, "ht-file-row");
      row.append(
        element(
          "span",
          `${i + 1}. ${f.name} (${(f.size / 1024 ** 2).toFixed(2)} MiB)`,
        ),
      );
      for (const [title, delta] of [
        ["上移", -1],
        ["下移", 1],
      ]) {
        const b = element("button", title, "ht-button");
        b.type = "button";
        b.disabled = i + delta < 0 || i + delta >= files.length;
        b.onclick = () => {
          [files[i], files[i + delta]] = [files[i + delta], files[i]];
          drawFiles();
        };
        row.append(b);
      }
      const remove = element("button", "移除", "ht-button");
      remove.type = "button";
      remove.onclick = () => {
        files.splice(i, 1);
        drawFiles();
      };
      row.append(remove);
      fileNames.append(row);
    });
  }
  if (
    [
      "pdf-workbench",
      "images-pdf",
      "media-info",
      "image-metadata",
      "media-clip",
    ].includes(name)
  ) {
    const picker = field("file", "选择本地文件");
    picker.type = "file";
    picker.multiple = ["pdf-workbench", "images-pdf"].includes(name);
    picker.accept =
      name === "pdf-workbench"
        ? ".pdf"
        : name === "images-pdf" || name === "image-metadata"
          ? "image/*"
          : "audio/*,video/*,.mkv,.mka,.flac,.aiff,.ts";
    picker.onchange = () => {
      files = picker.multiple ? [...files, ...picker.files] : [...picker.files];
      picker.value = "";
      drawFiles();
    };
    form.append(fileNames);
    drawFiles();
  }
  if (name === "pdf-workbench") {
    select("operation", "操作", [
      ["merge", "合并 PDF"],
      ["extract", "抽取指定页"],
      ["split", "按页拆分为 ZIP"],
      ["rotate", "旋转指定页"],
      ["info", "查看页数、尺寸、加密状态"],
    ]);
    field("pages", "页码（1,3-5；留空为全部）");
    select("rotation", "顺时针旋转角度", [
      ["90", "90°"],
      ["180", "180°"],
      ["270", "270°"],
    ]);
    form.append(
      element(
        "p",
        "合并按上方文件顺序；编辑保留页面内容，合并/抽页不保留整份文件的书签、签名或交互表单语义。",
        "ht-subtle",
      ),
    );
  }
  if (name === "config-diff") {
    select("format", "比较方式", [
      ["text", "文本：逐行比较"],
      ["json", "JSON：忽略对象键顺序与格式"],
      ["yaml", "YAML：忽略映射键顺序与格式"],
    ]);
    field("left", "修改前", "textarea");
    field("right", "修改后", "textarea");
    form.append(
      element(
        "p",
        "JSON / YAML 比较保留数组顺序与整数精度；注释、键顺序和格式差异请使用文本模式。",
        "ht-subtle",
      ),
    );
  }
  if (name === "cron-preview") {
    field(
      "expression",
      "Cron（5 字段，或含秒的 6 字段）",
      "input",
      "0 2 * * *",
    );
    field(
      "timezone",
      "IANA 时区",
      "input",
      Intl.DateTimeFormat().resolvedOptions().timeZone || "Asia/Shanghai",
    );
    field("start", "起算时间（ISO 8601，包含时区；留空为现在）");
  }
  if (name === "qr-generate") field("text", "二维码内容", "textarea");
  if (name === "media-clip") {
    field(
      "source",
      "或公开文件直链（已有文件时优先使用文件）",
      "input",
      action.arguments?.text || "",
    );
    field("start", "开始时间（秒）", "input", "0");
    field("end", "结束时间（秒）", "input", "10");
    select("mode", "截取方式", [
      ["copy", "快速无损 → MKV（关键帧边界）"],
      ["exact", "精确视频 → MP4（H.264 / AAC）"],
      ["audio", "仅音频 → M4A（AAC）"],
    ]);
  }
  const controls = element("div", null, "ht-controls"),
    run = element(
      "button",
      name === "media-clip" ? "上传 / 提交截取" : "开始处理",
    ),
    cancel = element("button", "停止本地处理");
  run.type = "submit";
  cancel.type = "button";
  cancel.hidden = true;
  cancel.onclick = () => {
    worker?.terminate();
    worker = null;
    run.disabled = false;
    cancel.hidden = true;
    out.replaceChildren(element("p", "已停止。"));
  };
  controls.append(run, cancel);
  if (onBack) {
    const back = element("button", "返回操作");
    back.type = "button";
    back.onclick = () => {
      cleanup();
      onBack();
    };
    controls.append(back);
  }
  form.append(controls);
  const download = (f) => {
    const a = element("a", "下载 " + f.name, "ht-button");
    const url = URL.createObjectURL(f.blob);
    urls.push(url);
    a.href = url;
    a.download = f.name;
    return a;
  };
  function finish(data) {
    if (disposed) return;
    worker?.terminate();
    worker = null;
    run.disabled = false;
    cancel.hidden = true;
    out.replaceChildren();
    if (data.error) {
      out.append(element("p", data.error, "ht-error"));
      return;
    }
    const r = data.result;
    out.append(element("pre", r.text || "处理完成。"));
    const buttons = element("div", null, "ht-controls");
    if (r.preview) {
      const img = element("img");
      const url = URL.createObjectURL(r.preview);
      urls.push(url);
      img.src = url;
      img.alt = "生成的二维码";
      img.width = 220;
      out.append(img);
    }
    if (r.text) {
      const copy = element("button", "复制结果");
      copy.onclick = async () => {
        try {
          await navigator.clipboard.writeText(r.text);
          copy.textContent = "已复制";
        } catch {
          copy.textContent = "请手动选择结果复制";
        }
      };
      buttons.append(copy);
    }
    for (const f of r.files || []) buttons.append(download(f));
    if (onHandoff) {
      const chat = element("button", "把结果交给糖包");
      chat.onclick = () => onHandoff(r.text);
      buttons.append(chat);
    }
    out.append(buttons);
  }
  form.onsubmit = async (event) => {
    event.preventDefault();
    if (disposed || run.disabled) return;
    const options = Object.fromEntries(
      Object.entries(fields)
        .filter(([k]) => k !== "file")
        .map(([k, v]) => [k, v.value]),
    );
    if (
      name === "cron-preview" &&
      options.start &&
      !/(Z|[+-]\d\d:\d\d)$/.test(options.start)
    ) {
      finish({ error: "起算时间请包含时区，例如 2026-11-06T12:00:00+08:00。" });
      return;
    }
    releaseURLs();
    out.replaceChildren(
      element(
        "p",
        name === "media-clip" ? "正在上传 / 提交…" : "正在本地处理…",
      ),
    );
    run.disabled = true;
    if (name === "media-clip") {
      try {
        const clip = {
          start: Number(options.start),
          end: Number(options.end),
          mode: options.mode,
        };
        if (
          !Number.isFinite(clip.start) ||
          !Number.isFinite(clip.end) ||
          clip.start < 0 ||
          clip.end <= clip.start
        )
          throw Error("请填写有效的起止秒数，结束时间应大于开始时间。");
        const identity = JSON.stringify(options);
        if (
          !submitted ||
          submitted.identity !== identity ||
          submitted.file !== files[0]
        )
          submitted = { identity, file: files[0], id: crypto.randomUUID() };
        const requestId = submitted.id;
        const data = files.length
          ? await api("/v1/files/media-clip", {
              body: files[0],
              headers: {
                "X-Request-ID": requestId,
                "X-Tool-Options": JSON.stringify(clip),
                "Content-Type": files[0].type || "application/octet-stream",
              },
              signal,
              timeout: 0,
            })
          : await api("/v1/execute", {
              body: JSON.stringify({
                protocolVersion: 1,
                name,
                arguments: { text: options.source, clip },
                requestId,
              }),
              signal,
              timeout: 0,
            });
        if (!disposed) {
          cleanup();
          taskView(parent, data.task.id, { onBack, onHandoff, signal });
        }
      } catch (error) {
        finish({ error: error.message });
      }
      return;
    }
    cancel.hidden = false;
    worker = new Worker(new URL("./local-worker.js", import.meta.url), {
      type: "module",
    });
    worker.onmessage = (event) => finish(event.data);
    worker.onerror = () =>
      finish({
        error:
          "本地处理失败，可能是文件损坏、浏览器资源不足或组件加载失败。请缩小文件后重试。",
      });
    worker.postMessage({ name, files, options });
  };
}
