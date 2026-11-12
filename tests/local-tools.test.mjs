import test from "node:test";
import assert from "node:assert/strict";
import { PDFDocument } from "pdf-lib";
import { unzipSync } from "fflate";
import { pdf, pages, compare, cron } from "../browser/operations.mjs";
const file = async (sizes) => {
  const d = await PDFDocument.create();
  for (const s of sizes) d.addPage(s);
  const f = new Blob([await d.save()]);
  f.name = "test.pdf";
  return f;
};
test("PDF merge/extract/split/rotate preserve selected page geometry and order", async () => {
  const a = await file([
      [200, 300],
      [400, 500],
    ]),
    b = await file([[600, 700]]);
  const merged = await pdf([a, b], { operation: "merge" }),
    read = async (r) => PDFDocument.load(await r.files[0].blob.arrayBuffer());
  assert.deepEqual(
    (await read(merged)).getPages().map((p) => p.getWidth()),
    [200, 400, 600],
  );
  const extracted = await read(
    await pdf([a], { operation: "extract", pages: "2,1" }),
  );
  assert.deepEqual(
    extracted.getPages().map((p) => p.getWidth()),
    [400, 200],
  );
  const split = await pdf([a], { operation: "split", pages: "" });
  const entries = unzipSync(
    new Uint8Array(await split.files[0].blob.arrayBuffer()),
  );
  assert.equal(Object.keys(entries).length, 2);
  assert.equal(
    (await PDFDocument.load(entries["page-0002.pdf"])).getPage(0).getWidth(),
    400,
  );
  const rotated = await read(
    await pdf([a], { operation: "rotate", pages: "2", rotation: "90" }),
  );
  assert.equal(rotated.getPage(0).getRotation().angle, 0);
  assert.equal(rotated.getPage(1).getRotation().angle, 90);
  const info = JSON.parse((await pdf([a], { operation: "info" })).text);
  assert.equal(info.pages, 2);
  assert.equal(info.encrypted, false);
  assert.throws(() => pages("0,3", 2));
  assert.throws(() => pages("2-1", 2));
  assert.throws(() => pages("1;alert(1)", 2));
  await assert.rejects(() =>
    pdf([new Blob(["garbage"])], { operation: "info" }),
  );
});
test("structured diff distinguishes large integers and array order while ignoring map order", () => {
  assert.equal(
    compare({
      format: "json",
      left: '{"x":9007199254740993,"y":2}',
      right: '{"y":2,"x":9007199254740993}',
    }).text,
    "内容一致。",
  );
  assert.notEqual(
    compare({
      format: "json",
      left: '{"x":9007199254740993}',
      right: '{"x":9007199254740992}',
    }).text,
    "内容一致。",
  );
  assert.equal(
    compare({
      format: "yaml",
      left: "name: hello\nx: 1\n",
      right: "x: 1\nname: hello\n",
    }).text,
    "内容一致。",
  );
  assert.notEqual(
    compare({ format: "yaml", left: "x: [1,2]", right: "x: [2,1]" }).text,
    "内容一致。",
  );
  assert.throws(() =>
    compare({ format: "yaml", left: "a: 1\na: 2", right: "a: 1" }),
  );
  assert.match(
    compare({ format: "text", left: "a\nb", right: "a\nc" }).text,
    /-b[\s\S]*\+c/,
  );
});
test("cron respects selected timezone, DST and invalid expressions", () => {
  const result = cron({
    expression: "0 9 * * *",
    timezone: "Asia/Shanghai",
    start: "2026-11-06T00:00:00Z",
  });
  assert.match(result.text, /2026-11-06T01:00:00.000Z/);
  const dst = cron({
    expression: "0 9 * * *",
    timezone: "America/New_York",
    start: "2026-10-31T00:00:00Z",
  });
  assert.match(dst.text, /2026-10-31T13:00:00.000Z/);
  assert.match(dst.text, /2026-11-01T14:00:00.000Z/);
  assert.throws(() => cron({ expression: "* * *", timezone: "Asia/Shanghai" }));
  assert.throws(() => cron({ expression: "* * * * *", timezone: "invalid" }));
});

test("structured diff retains decimal source precision and rejects cyclic YAML", () => {
  assert.notEqual(
    compare({
      format: "json",
      left: '{"n":0.12345678901234567890}',
      right: '{"n":0.12345678901234567891}',
    }).text,
    "内容一致。",
  );
  assert.throws(() =>
    compare({ format: "yaml", left: "a: &a [*a]", right: "a: 1" }),
  );
});
