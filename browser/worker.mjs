import { pdf, imagesPDF, compare, cron, metadata, qr } from "./operations.mjs";
import mediaInfoFactory from "mediainfo.js";
self.onmessage = async ({ data: { name, files, options } }) => {
  try {
    let result;
    switch (name) {
      case "pdf-workbench":
        result = await pdf(files, options);
        break;
      case "images-pdf":
        result = await imagesPDF(files);
        break;
      case "config-diff":
        result = compare(options);
        break;
      case "cron-preview":
        result = cron(options);
        break;
      case "image-metadata":
        result = await metadata(files);
        break;
      case "qr-generate":
        result = await qr(options);
        break;
      case "media-info": {
        const file = files[0];
        if (!file) throw Error("请先选择音视频文件。");
        const info = await mediaInfoFactory({
          format: "object",
          locateFile: () =>
            new URL("./MediaInfoModule.wasm", self.location.href).href,
        });
        let read = 0;
        try {
          const data = await info.analyzeData(
            () => file.size,
            async (size, offset) => {
              const chunk = new Uint8Array(
                await file.slice(offset, offset + size).arrayBuffer(),
              );
              read += chunk.length;
              return chunk;
            },
          );
          if (
            !data.media?.track?.some(
              (t) => t["@type"] === "Audio" || t["@type"] === "Video",
            )
          )
            throw Error("未发现可识别的音频或视频轨道。");
          result = {
            text:
              `本地读取 ${(read / 1024 ** 2).toFixed(2)} MiB / 文件 ${(file.size / 1024 ** 2).toFixed(2)} MiB；没有上传文件。\n\n` +
              JSON.stringify(data, null, 2),
          };
        } finally {
          info.close();
        }
        break;
      }
      default:
        throw Error("不支持的本地工具。");
    }
    self.postMessage({ result });
  } catch (error) {
    self.postMessage({ error: error.message || "文件处理失败。" });
  }
};
