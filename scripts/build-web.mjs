import { build } from "esbuild";
import {
  copyFile,
  writeFile,
  readFile,
  mkdir,
  readdir,
} from "node:fs/promises";
const output = "internal/toolkit/web";
await build({
  entryPoints: ["browser/worker.mjs"],
  outfile: output + "/local-worker.js",
  bundle: true,
  format: "esm",
  platform: "browser",
  target: ["es2022"],
  minify: true,
  legalComments: "linked",
});
await copyFile(
  "node_modules/mediainfo.js/dist/MediaInfoModule.wasm",
  output + "/MediaInfoModule.wasm",
);
// Ship upstream license notices with the same-origin bundle.
const notices = [];
async function licenses(dir) {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    if (!entry.isDirectory() || entry.name.startsWith(".")) continue;
    const path = dir + "/" + entry.name;
    if (entry.name.startsWith("@")) {
      await licenses(path);
      continue;
    }
    try {
      const pkg = JSON.parse(await readFile(path + "/package.json", "utf8"));
      const files = (await readdir(path)).filter((n) =>
        /^(licen[cs]e|copying|notice)(\.|$)/i.test(n),
      );
      for (const f of files)
        notices.push(
          `\n--- ${pkg.name}@${pkg.version} (${pkg.license || ""}) ---\n` +
            (await readFile(path + "/" + f, "utf8")),
        );
    } catch {}
  }
}
await licenses("node_modules");
await writeFile(output + "/THIRD-PARTY-NOTICES.txt", notices.sort().join("\n"));
