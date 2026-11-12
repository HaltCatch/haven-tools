import test from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { renderEditor } from "../internal/toolkit/web/editor.js";

// Import the actual ES module: select.type is read-only in browsers and
// non-strict eval fixtures silently hide the assignment failure.
test("every editor renders a complete form with usable default selections", () => {
  const dom = new JSDOM("<main></main>");
  const previous = {
    document: globalThis.document,
    MutationObserver: globalThis.MutationObserver,
  };
  globalThis.document = dom.window.document;
  globalThis.MutationObserver = dom.window.MutationObserver;
  try {
    for (const name of [
      "pdf-workbench",
      "images-pdf",
      "config-diff",
      "cron-preview",
      "media-info",
      "image-metadata",
      "qr-generate",
      "media-clip",
    ]) {
      const controller = new AbortController();
      const parent = document.querySelector("main");
      try {
        renderEditor(parent, {
          action: { handlerId: name, title: name },
          signal: controller.signal,
        });
        assert.ok(
          parent.querySelector("button[type=submit]"),
          name + ": submit button",
        );
        for (const select of parent.querySelectorAll("select")) {
          assert.ok(select.options.length > 1, name + ": choices");
          assert.equal(
            select.value,
            select.options[0].value,
            name + ": default",
          );
          select.selectedIndex = 1;
          assert.equal(
            select.value,
            select.options[1].value,
            name + ": change choice",
          );
        }
      } finally {
        controller.abort();
      }
    }
  } finally {
    Object.assign(globalThis, previous);
    dom.window.close();
  }
});
