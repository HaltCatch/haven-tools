import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";

const source = readFileSync(
  new URL("../internal/toolkit/web/client.js", import.meta.url),
  "utf8",
);
const appSource = readFileSync(
  new URL("../internal/toolkit/web/app.js", import.meta.url),
  "utf8",
);
const pause = () => new Promise((resolve) => setTimeout(resolve, 5));
async function wait(check) {
  for (let i = 0; i < 300; i++) {
    if (check()) return;
    await pause();
  }
  throw Error("Expected UI state did not arrive");
}
function fixture({ path = "/tools/", fetch } = {}) {
  const dom = new JSDOM('<main><section id="workspace"></section></main>', {
      url: "https://homepage.example" + path,
      runScripts: "outside-only",
    }),
    w = dom.window;
  w.AbortController = AbortController;
  w.fetch =
    fetch ||
    (() => {
      throw Error("Unexpected network call");
    });
  w.crypto.randomUUID = () => "test-request-id";
  Object.defineProperty(w.navigator, "clipboard", {
    value: { writeText: async () => {} },
  });
  w.eval(
    "(function(){" +
      source
        .replaceAll(
          "import.meta.url",
          JSON.stringify("https://homepage.example/tools/assets/client.js"),
        )
        .replaceAll("export ", "") +
      ";window.tools={api,base,renderResult,icon,element,execute,suggest,taskView,showError};})();",
  );
  return {
    w,
    tools: w.tools,
    workspace: w.document.getElementById("workspace"),
    close: () => w.close(),
    load: () =>
      w.eval(
        appSource.replace(
          /^import[\s\S]*?from "\.\/client\.js";/,
          "const {api,base,renderResult,icon,element,execute,suggest,taskView,showError}=window.tools;",
        ),
      ),
  };
}
const json = (data, status = 200) =>
  new Response(JSON.stringify({ protocolVersion: 1, ...data }), {
    status,
    headers: { "content-type": "application/json" },
  });
const result = {
  type: "text",
  title: "结果",
  text: "原始内容 <script>不是代码</script>",
  icon: { name: "test", paths: ["M3 3h18v18H3z"] },
};

test("one renderer handles text, JSON and field results without HTML or executable icons", () => {
  const app = fixture();
  try {
    for (const type of ["text", "json", "fields"]) {
      app.tools.renderResult(app.workspace, {
        ...result,
        type,
        description: "<img src=x onerror=alert(1)>",
        fields: [{ label: "UTC", value: "2026-10-24" }],
        links: [
          { label: "unsafe", href: "javascript:alert(1)" },
          { label: "source", href: "https://example.com" },
        ],
      });
      assert.equal(app.workspace.querySelector("script,img"), null);
      assert.equal(app.workspace.querySelectorAll("a").length, 1);
      assert.equal(app.workspace.querySelector("a").rel, "noopener noreferrer");
      assert.ok(app.workspace.querySelector(type === "fields" ? "dl" : "pre"));
    }
    assert.equal(
      app.tools.icon({ paths: ["<script>alert(1)</script>"] }).children.length,
      0,
    );
    assert.throws(() =>
      app.tools.renderResult(app.workspace, { ...result, type: "html" }),
    );
    assert.equal(
      app.w.document.querySelectorAll("link[data-haven-tools]").length,
      1,
    );
  } finally {
    app.close();
  }
});

test("suggestions perform no execution and explicit execution keeps its request ID", async () => {
  const calls = [];
  const app = fixture({
    fetch: async (url, options) => {
      calls.push({ url, ...options });
      return json(url.endsWith("/suggest") ? { actions: [] } : { result });
    },
  });
  try {
    await app.tools.suggest("base64 私人内容", "text");
    assert.equal(calls.length, 1);
    assert.ok(calls[0].url.endsWith("/suggest"));
    const action = {
      handlerId: "base64-encode",
      arguments: { text: "私人内容" },
    };
    for (let i = 0; i < 2; i++)
      await app.tools.execute(action, null, undefined, "stable-retry-id");
    for (const call of calls.slice(1))
      assert.equal(JSON.parse(call.body).requestId, "stable-retry-id");
    assert.ok(
      calls.every(
        (call) => !call.url.includes("model") && !call.headers.Authorization,
      ),
    );
  } finally {
    app.close();
  }
});

test("authentication HTML, protocol mismatch and tool failures remain explicit errors", async () => {
  for (const response of [
    new Response("<html>login</html>", {
      headers: { "content-type": "text/html" },
    }),
    new Response('{"protocolVersion":999}', {
      headers: { "content-type": "application/json" },
    }),
    json({ error: "实际失败" }, 422),
  ]) {
    const app = fixture({ fetch: async () => response });
    try {
      await assert.rejects(app.tools.api("/v1/catalog"));
    } finally {
      app.close();
    }
  }
});

test("closing a result viewer cancels polling without canceling a background task", async () => {
  const calls = [];
  let finish;
  const app = fixture({
    fetch: async (url, options) => {
      calls.push(url);
      return new Promise((resolve, reject) => {
        finish = resolve;
        options.signal.addEventListener(
          "abort",
          () => reject(new DOMException("Cancelled", "AbortError")),
          { once: true },
        );
      });
    },
  });
  try {
    const controller = new AbortController();
    const pending = app.tools.taskView(app.workspace, "task-id", {
      signal: controller.signal,
    });
    await pause();
    controller.abort();
    await pending;
    finish?.(json({ task: { state: "succeeded", result } }));
    await pause();
    assert.equal(calls.length, 1);
    assert.equal(
      calls.some((url) => url.endsWith("/cancel")),
      false,
    );
    assert.equal(app.workspace.textContent, "");
  } finally {
    app.close();
  }
});

test("background completion shows the durable result and stable link in the same viewer", async () => {
  const app = fixture({
    fetch: async () =>
      json({
        task: {
          id: "task-id",
          title: "后台任务",
          state: "succeeded",
          createdAt: "2026-10-24T00:00:00Z",
          updatedAt: "2026-10-24T00:01:00Z",
          href: "https://homepage.example/tools/tasks/task-id",
          result,
        },
      }),
  });
  try {
    await app.tools.taskView(app.workspace, "task-id");
    assert.match(app.workspace.textContent, /已完成/);
    assert.match(app.workspace.textContent, /原始内容/);
    assert.equal(
      app.workspace.querySelector("a").href,
      "https://homepage.example/tools/tasks/task-id",
    );
    assert.match(app.workspace.textContent, /复制结果链接/);
  } finally {
    app.close();
  }
});

test("independent tool page routes input to the service and keeps IME and multiline behavior", async () => {
  const calls = [];
  const app = fixture({
    fetch: async (url, options) => {
      calls.push(url);
      if (url.endsWith("/catalog")) return json({ tools: [{ name: "calculate", title: "计算", example: "(2+3)×4", mode: "sync", inputKind: "text", icon: result.icon }] });
      if (url.endsWith("/suggest"))
        return json({
          actions: [
            {
              id: "x",
              kind: "remote",
              handlerId: "calculate",
              title: "= 20",
              detail: "计算",
              arguments: { text: "(2+3)×4" },
              icon: result.icon,
            },
          ],
        });
      return json({ result: { ...result, text: "20" } });
    },
  });
  try {
    app.load();
    await wait(() => app.workspace.querySelector('[role="option"]'));
    const input = app.workspace.querySelector("textarea");
    input.value = "(2+3)×4";
    input.dispatchEvent(new app.w.Event("input"));
    const composing = new app.w.KeyboardEvent("keydown", {
      key: "Enter",
      isComposing: true,
      cancelable: true,
    });
    input.dispatchEvent(composing);
    assert.equal(
      calls.some((url) => url.endsWith("/execute")),
      false,
    );
    const newline = new app.w.KeyboardEvent("keydown", {
      key: "Enter",
      shiftKey: true,
      cancelable: true,
    });
    input.dispatchEvent(newline);
    assert.equal(newline.defaultPrevented, false);
    input.dispatchEvent(
      new app.w.KeyboardEvent("keydown", { key: "Enter", cancelable: true }),
    );
    await wait(() => app.workspace.querySelector(".ht-result"));
    assert.equal(app.workspace.querySelector("pre").textContent, "20");
    assert.equal(calls.filter((url) => url.endsWith("/execute")).length, 1);
  } finally {
    app.close();
  }
});

test("PDF upload uses declared endpoint with stable ID, expired files hide downloads but keep text", async () => {
  const app = fixture({
    fetch: async (url, options) => {
      if (url.includes("/v1/files/")) {
        assert.equal(url, "/tools/v1/files/document-text");
        assert.equal(options.headers["X-Request-ID"], "same-request");
        assert.equal(options.body.type, "application/pdf");
        return json({ task: { id: "uploaded" } }, 202);
      }
      return json({
        task: {
          id: "expired",
          title: "OCR",
          state: "succeeded",
          createdAt: "2026-10-24T00:00:00Z",
          updatedAt: "2026-10-24T00:00:01Z",
          href: "/tools/tasks/expired",
          filesExpireAt: "2026-10-23T00:00:00Z",
          filesAvailable: false,
          result: {
            ...result,
            links: [
              {
                label: "download",
                href: "/tools/tasks/expired/files/text.txt",
              },
              { label: "source", href: "https://example.com/article" },
            ],
          },
        },
      });
    },
  });
  try {
    const file = new app.w.File(["pdf"], "test.pdf", {
      type: "application/pdf",
    });
    await app.tools.execute(
      { inputKind: "file", uploadPath: "/v1/files/document-text" },
      file,
      undefined,
      "same-request",
    );
    await app.tools.taskView(app.workspace, "expired");
    assert.match(app.workspace.textContent, /文字结果仍保留/);
    assert.match(app.workspace.textContent, /原始内容/);
    assert.equal(
      [...app.workspace.querySelectorAll("a")].some(
        (a) => a.textContent === "download",
      ),
      false,
    );
    assert.equal(
      [...app.workspace.querySelectorAll("a")].some(
        (a) => a.textContent === "source",
      ),
      true,
    );
  } finally {
    app.close();
  }
});


test("media upload passes an unrestricted file without a MIME or byte-size gate", async () => {
  const file = { name: "recording.unknown", type: "", size: 200 * 1024 * 1024 };
  let received;
  const app = fixture({ fetch: async (url, options) => {
    assert.equal(url, "/tools/v1/files/media-transcribe");
    received = options.body;
    assert.equal(options.headers["Content-Type"], "application/octet-stream");
    return json({ task: { id: "accepted" } }, 202);
  }});
  try {
    const data = await app.tools.execute({ inputKind: "file", uploadPath: "/v1/files/media-transcribe" }, file);
    assert.equal(received, file);
    assert.equal(data.task.id, "accepted");
  } finally { app.close(); }
});

test("proxy upload rejection is distinct from an expired login", async () => {
  const app = fixture({ fetch: async () => new Response("too large", {status: 413}) });
  try { await assert.rejects(app.tools.api("/v1/files/media-audio"), /入口代理或上游服务/); }
  finally { app.close(); }
});


test("catalog is visible on entry, all-tools preserves input, and attachment is an accessible icon", async () => {
  const calls = [];
  const tools = ["json-format", "json-compact"].map(name => ({
    name, title: name, category: "数据", description: "处理配置",
    mode: "sync", inputKind: "text", example: "json {}", icon: result.icon,
  }));
  const app = fixture({ fetch: async (url, options) => {
    calls.push(url);
    if (url.endsWith("/catalog")) return json({ tools });
    assert.ok(url.endsWith("/suggest"));
    return json({ actions: [] });
  }});
  try {
    app.load();
    await wait(() => app.workspace.querySelectorAll(".tool-grid .option").length === 2);
    assert.equal(calls[0], "/tools/v1/catalog");
    const input = app.workspace.querySelector("textarea");
    assert.equal(input.value, "");
    const attach = app.workspace.querySelector(".attach-button");
    assert.equal(attach.textContent, "");
    assert.equal(attach.getAttribute("aria-label"), "添加附件");
    assert.ok(attach.querySelector("svg path"));
    let picked = false;
    app.workspace.querySelector('input[type="file"]').click = () => { picked = true; };
    attach.click(); assert.equal(picked, true);
    input.value = "保留这段内容";
    input.dispatchEvent(new app.w.Event("input"));
    await wait(() => app.workspace.querySelector(".empty"));
    app.workspace.querySelector(".browse-button").click();
    await wait(() => app.workspace.querySelectorAll(".tool-grid .option").length === 2);
    assert.equal(input.value, "保留这段内容");
    assert.equal(calls.some(url => url.endsWith("/execute")), false);
    app.workspace.querySelector(".option").click();
    assert.equal(input.value, "json {}");
  } finally { app.close(); }
});
