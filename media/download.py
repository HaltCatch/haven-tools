"""Fixed Bilibili adapter around yt-dlp's public API. No arbitrary CLI options."""
import json
import os
import pathlib
import re
import sys
import urllib.parse

import yt_dlp



class QuietLogger:
    def debug(self, _message):
        pass

    info = warning = error = debug


def validate_url(value):
    url = urllib.parse.urlsplit(value)
    if (
        url.scheme != "https"
        or url.netloc != "www.bilibili.com"
        or not re.fullmatch(r"/video/BV[0-9A-Za-z]{10}", url.path)
        or (url.query and not re.fullmatch(r"p=[1-9][0-9]{0,3}", url.query))
        or url.fragment
    ):
        raise ValueError("Only canonical Bilibili video URLs are supported")


def download(value, directory):
    validate_url(value)
    root = pathlib.Path(directory).resolve(strict=True)

    options = {
        "format": "bv[height<=480][ext=mp4][vcodec^=avc1]+ba[ext=m4a]/b[height<=480][ext=mp4]",
        "outtmpl": str(root / "video.%(ext)s"),
        "merge_output_format": "mp4",
        "noplaylist": True,
        "playlist_items": "1",
        "cachedir": False,
        "quiet": True,
        "no_warnings": True,
        "logger": QuietLogger(),
        "socket_timeout": 20,
        "retries": 2,
        "fragment_retries": 2,
        "concurrent_fragment_downloads": 1,
        # Only this extractor, rather than a general URL execution surface.
        "allowed_extractors": ["BiliBili"],
        "overwrites": False,
        "postprocessor_args": {"Merger+ffmpeg_o": ["-threads", "1"]},
    }
    with yt_dlp.YoutubeDL(options) as downloader:
        info = downloader.extract_info(value, download=True)
    video = root / "video.mp4"
    if not info or not video.is_file() or video.is_symlink():
        raise ValueError("Video download did not finish")
    size = video.stat().st_size
    if size <= 0 or not info.get("duration", 0) > 0:
        raise ValueError("Video download is empty or has invalid metadata")
    os.chmod(video, 0o600)
    # Signed CDN URLs, cookies and full extractor output never leave the adapter.
    return {"title": info.get("title", "B 站视频"), "duration": info["duration"], "bytes": size}


if __name__ == "__main__":
    try:
        print(json.dumps(download(sys.argv[1], sys.argv[2]), ensure_ascii=False))
    except Exception:
        print("Bilibili download failed", file=sys.stderr)
        sys.exit(1)
