"""Bounded local image conversion with Pillow; text extraction uses shared OCR."""
import json
import os
from pathlib import Path
import resource
import sys
import warnings
from PIL import Image, ImageOps

Image.MAX_IMAGE_PIXELS = 32_000_000
warnings.simplefilter("error", Image.DecompressionBombWarning)


def image(path):
    with Image.open(path) as source:
        if source.width * source.height > Image.MAX_IMAGE_PIXELS:
            raise ValueError("image too large")
        source.seek(0)
        result = ImageOps.exif_transpose(source).copy()
    return result


def process(operation, source, destination):
    destination = Path(destination)
    im = image(source)
    original = f"{im.width} × {im.height}"
    fmt = {"image-png": "PNG", "image-jpeg": "JPEG", "image-webp": "WEBP",
           "image-compress": "WEBP", "image-resize": "WEBP"}[operation]
    if operation == "image-resize":
        im.thumbnail((1920, 1920), Image.Resampling.LANCZOS)
    if fmt == "JPEG":
        if "A" in im.getbands() or "transparency" in im.info:
            rgba = im.convert("RGBA")
            bg = Image.new("RGB", im.size, "white")
            bg.paste(rgba, mask=rgba.getchannel("A"))
            im = bg
        else:
            im = im.convert("RGB")
    elif im.mode not in ("RGB", "RGBA", "L", "LA"):
        im = im.convert("RGBA")
    # Write only pixels: no EXIF/GPS or arbitrary embedded metadata.
    clean = Image.new(im.mode, im.size)
    clean.paste(im)
    filename = "image." + {"PNG": "png", "JPEG": "jpg", "WEBP": "webp"}[fmt]
    options = {"quality": 80 if operation == "image-compress" else 90} if fmt != "PNG" else {}
    clean.save(destination / filename, fmt, **options)
    size = (destination / filename).stat().st_size
    return dict(text="图片处理完成。多帧图片仅处理第一帧，输出已移除 EXIF 等元数据。",
                fields=[{"label": "原始尺寸", "value": original},
                        {"label": "结果尺寸", "value": f"{im.width} × {im.height}"},
                        {"label": "格式", "value": fmt},
                        {"label": "大小", "value": f"{size / 1024:.1f} KiB"}],
                file=filename, contentType="image/" + {"PNG": "png", "JPEG": "jpeg", "WEBP": "webp"}[fmt])


if __name__ == "__main__":
    resource.setrlimit(resource.RLIMIT_AS, (512 << 20, 512 << 20))
    resource.setrlimit(resource.RLIMIT_CPU, (600, 600))
    result = process(*sys.argv[1:])
    print(json.dumps(result, ensure_ascii=False))
