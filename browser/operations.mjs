import { PDFDocument, degrees } from "pdf-lib";
import { zipSync } from "fflate";
import { createTwoFilesPatch } from "diff";
import { parseDocument, visit } from "yaml";
import { CronExpressionParser } from "cron-parser";
import exifr from "exifr";
import QRCode from "qrcode";

const output = (name, data, type) => ({
  name,
  blob: new Blob([data], { type }),
});
export function pages(value, count) {
  if (!value.trim()) return Array.from({ length: count }, (_, i) => i);
  const result = [];
  for (const part of value.split(",")) {
    const m = part.trim().match(/^(\d+)(?:-(\d+))?$/);
    if (!m) throw Error("页码格式：1,3-5；留空代表全部页。");
    const start = Number(m[1]),
      end = Number(m[2] || m[1]);
    if (start < 1 || end < start || end > count)
      throw Error(`页码超出范围（1–${count}）。`);
    for (let i = start; i <= end; i++) result.push(i - 1);
    if (result.length > 5000) throw Error("一次最多选择 5000 页。");
  }
  return result;
}
function checkFiles(files, max = 512 * 1024 ** 2) {
  if (!files?.length) throw Error("请先选择文件。");
  if (files.length > 100 || files.reduce((n, f) => n + f.size, 0) > max)
    throw Error("本地处理最多 100 个文件、合计 512 MiB；请分批处理。");
}
export async function pdf(files, options) {
  checkFiles(files);
  if (options.operation !== "merge" && files.length !== 1)
    throw Error("此操作请选择一个 PDF。");
  const docs = [];
  for (const f of files) {
    const doc = await PDFDocument.load(await f.arrayBuffer(), {
      ignoreEncryption: true,
      updateMetadata: false,
    });
    if (doc.isEncrypted) {
      if (options.operation === "info")
        return {
          text:
            "加密：是\n文件大小：" +
            f.size +
            " 字节\n加密文件的页数与尺寸可能不可读；本工具不解密文件。",
        };
      throw Error("文件已加密。请先用有权限的 PDF 阅读器解密后再处理。");
    }
    docs.push(doc);
  }
  const doc = docs[0];
  if (options.operation === "info")
    return {
      text: JSON.stringify(
        {
          pages: doc.getPageCount(),
          bytes: files[0].size,
          encrypted: false,
          title: doc.getTitle(),
          author: doc.getAuthor(),
          pageSizes: doc.getPages().map((p, i) => ({
            page: i + 1,
            widthPt: p.getWidth(),
            heightPt: p.getHeight(),
            rotation: p.getRotation().angle,
          })),
        },
        null,
        2,
      ),
    };
  if (options.operation === "rotate") {
    for (const i of pages(options.pages || "", doc.getPageCount()))
      doc
        .getPage(i)
        .setRotation(
          degrees(
            doc.getPage(i).getRotation().angle + Number(options.rotation),
          ),
        );
    return {
      text: "旋转完成。",
      files: [output("rotated.pdf", await doc.save(), "application/pdf")],
    };
  }
  if (options.operation === "split") {
    const entries = {};
    for (const i of pages(options.pages || "", doc.getPageCount())) {
      const single = await PDFDocument.create();
      for (const p of await single.copyPages(doc, [i])) single.addPage(p);
      entries[`page-${String(i + 1).padStart(4, "0")}.pdf`] =
        await single.save();
    }
    return {
      text: "每页一个 PDF，打包下载。",
      files: [
        output("pages.zip", zipSync(entries, { level: 0 }), "application/zip"),
      ],
    };
  }
  const target = await PDFDocument.create();
  for (const source of docs)
    for (const p of await target.copyPages(
      source,
      options.operation === "merge"
        ? source.getPageIndices()
        : pages(options.pages || "", source.getPageCount()),
    ))
      target.addPage(p);
  return {
    text: `已生成 ${target.getPageCount()} 页 PDF。合并按文件列表顺序；抽页按填写的页码顺序。`,
    files: [output("result.pdf", await target.save(), "application/pdf")],
  };
}
export async function imagesPDF(files) {
  checkFiles(files, 256 * 1024 ** 2);
  const doc = await PDFDocument.create();
  for (const f of files) {
    const bitmap = await createImageBitmap(f);
    try {
      if (bitmap.width * bitmap.height > 32000000)
        throw Error("单张图片最多 3200 万像素。");
      const canvas = new OffscreenCanvas(bitmap.width, bitmap.height);
      const ctx = canvas.getContext("2d");
      ctx.fillStyle = "white";
      ctx.fillRect(0, 0, canvas.width, canvas.height);
      ctx.drawImage(bitmap, 0, 0);
      const blob = await canvas.convertToBlob({
        type: "image/jpeg",
        quality: 0.95,
      });
      const img = await doc.embedJpg(await blob.arrayBuffer());
      const scale = Math.min(1, 595.28 / img.width, 841.89 / img.height);
      const page = doc.addPage([img.width * scale, img.height * scale]);
      page.drawImage(img, {
        x: 0,
        y: 0,
        width: page.getWidth(),
        height: page.getHeight(),
      });
    } finally {
      bitmap.close();
    }
  }
  return {
    text: "每张图片一页，保持比例，最大 A4，透明背景填白。按列表顺序合成。",
    files: [output("images.pdf", await doc.save(), "application/pdf")],
  };
}
function canonical(text, mode) {
  if (mode === "text") return text;
  // YAML JSON schema handles JSON too; BigInt preserves values past 2^53.
  const doc = parseDocument(text, {
    schema: mode === "json" ? "json" : "core",
    intAsBigInt: true,
    uniqueKeys: true,
  });
  if (doc.errors.length) throw Error(doc.errors[0].message);
  if (mode === "json") JSON.parse(text); // Syntax only; never use its lossy number values.
  class ExactNumber {
    constructor(source) {
      this.source = source;
    }
  }
  visit(doc, {
    Scalar(_key, node) {
      if (typeof node.value === "number") {
        if (!Number.isFinite(node.value))
          throw Error("非有限数字请使用文本模式比较。");
        node.value = new ExactNumber(node.source.replaceAll("_", ""));
      }
    },
  });
  const value = doc.toJS({ maxAliasCount: 100 });
  const active = new WeakSet();
  const stable = (v, depth = 0) => {
    if (depth > 100) throw Error("配置嵌套超过 100 层。");
    if (v instanceof ExactNumber) return v.source;
    if (v && typeof v === "object") {
      if (active.has(v)) throw Error("配置含循环引用，请使用文本模式比较。");
      active.add(v);
    }
    const indent = "  ".repeat(depth),
      next = "  ".repeat(depth + 1);
    let result;

    if (typeof v === "bigint") return v.toString();
    if (Array.isArray(v))
      result = v.length
        ? "[\n" +
          next +
          v.map((x) => stable(x, depth + 1)).join(",\n" + next) +
          "\n" +
          indent +
          "]"
        : "[]";
    else if (v && typeof v === "object") {
      const keys = Object.keys(v).sort();
      result = keys.length
        ? "{\n" +
          next +
          keys
            .map((k) => JSON.stringify(k) + ": " + stable(v[k], depth + 1))
            .join(",\n" + next) +
          "\n" +
          indent +
          "}"
        : "{}";
    } else result = JSON.stringify(v);
    if (v && typeof v === "object") active.delete(v);
    return result;
  };
  return stable(value) + "\n";
}
export function compare({ left, right, format }) {
  if (left.length + right.length > 1000000)
    throw Error("两份文本合计最多 100 万字符。");
  const a = canonical(left, format),
    b = canonical(right, format);
  const patch =
    a === b
      ? "内容一致。"
      : createTwoFilesPatch("修改前", "修改后", a, b, "", "", {
          context: 4,
          timeout: 2000,
          maxEditLength: 10000,
        });
  if (!patch) throw Error("差异过多，请缩小比较范围。");
  return {
    text: patch,
    files: [output("changes.diff", patch, "text/plain;charset=utf-8")],
  };
}
export function cron({ expression, timezone, start }) {
  if (![5, 6].includes(expression.trim().split(/\s+/).length))
    throw Error("Cron 请填写 5 个字段，或包含秒的 6 个字段。");
  new Intl.DateTimeFormat("zh-CN", { timeZone: timezone });
  const schedule = CronExpressionParser.parse(expression, {
    tz: timezone,
    currentDate: start || new Date(),
  });
  const format = new Intl.DateTimeFormat("zh-CN", {
    timeZone: timezone,
    dateStyle: "full",
    timeStyle: "long",
  });
  const dates = Array.from({ length: 10 }, () => schedule.next().toDate());
  return {
    text:
      `时区：${timezone}\n` +
      dates
        .map((d, i) => `${i + 1}. ${format.format(d)}\n   ${d.toISOString()}`)
        .join("\n"),
  };
}
export async function metadata(files) {
  checkFiles(files);
  const data = await exifr.parse(files[0], {
    tiff: true,
    exif: true,
    gps: true,
    xmp: true,
    icc: true,
    iptc: true,
    jfif: true,
    ihdr: true,
  });
  return {
    text: data
      ? JSON.stringify(data, null, 2)
      : "未发现可读取的 EXIF / XMP 等元数据。截图或处理过的图片可能没有这些信息。",
  };
}
export async function qr({ text }) {
  if (!text.trim()) throw Error("请输入二维码内容。");
  const svg = await QRCode.toString(text, {
    type: "svg",
    errorCorrectionLevel: "M",
    margin: 4,
  });
  const encoded = QRCode.create(text, { errorCorrectionLevel: "M" });
  const n = encoded.modules.size,
    scale = 8,
    canvas = new OffscreenCanvas((n + 8) * scale, (n + 8) * scale),
    ctx = canvas.getContext("2d");
  ctx.fillStyle = "#fff";
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  ctx.fillStyle = "#000";
  for (let y = 0; y < n; y++)
    for (let x = 0; x < n; x++)
      if (encoded.modules.get(y, x))
        ctx.fillRect((x + 4) * scale, (y + 4) * scale, scale, scale);
  const png = await canvas.convertToBlob({ type: "image/png" });
  return {
    text: "二维码已生成；内容不会自动打开。",
    files: [
      { name: "qrcode.png", blob: png },
      output("qrcode.svg", svg, "image/svg+xml"),
    ],
    preview: png,
  };
}
