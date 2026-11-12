import importlib.util
import pathlib
import sys
import tempfile
import types
import unittest
from unittest.mock import patch

# The adapter contract tests use a fake upstream; CI makes no real downloads.
spec = importlib.util.spec_from_file_location("download", pathlib.Path(__file__).with_name("download.py"))
adapter = importlib.util.module_from_spec(spec)
with patch.dict(sys.modules, {"yt_dlp": types.SimpleNamespace()}):
    spec.loader.exec_module(adapter)


class DownloadContract(unittest.TestCase):
    def test_url_surface_is_bilibili_single_video_only(self):
        for value in ["https://www.bilibili.com/video/BV1MT41127uH", "https://www.bilibili.com/video/BV1MT41127uH?p=2"]:
            adapter.validate_url(value)
        for value in ["https://127.0.0.1/video/BV1MT41127uH", "https://www.bilibili.com@evil.test/video/BV1MT41127uH", "http://www.bilibili.com/video/BV1MT41127uH", "https://www.bilibili.com/video/BV1MT41127uH?exec=x", "https://www.bilibili.com/video/BV1MT41127uH?p=0"]:
            with self.assertRaises(ValueError):
                adapter.validate_url(value)

    def test_native_options_no_local_size_duration_caps_and_redacted_output(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            observed = {}

            class Downloader:
                def __init__(self, options):
                    observed.update(options)

                def __enter__(self):
                    return self

                def __exit__(self, *args):
                    pass

                def extract_info(self, value, download):
                    (root / "video.mp4").write_bytes(b"video")
                    return {"title": "视频", "duration": 7200, "url": "https://cdn.example/signed-secret", "http_headers": {"Cookie": "secret"}}

            with patch.object(adapter, "yt_dlp", types.SimpleNamespace(YoutubeDL=Downloader)):
                result = adapter.download("https://www.bilibili.com/video/BV1MT41127uH", directory)
            self.assertEqual(result, {"title": "视频", "duration": 7200, "bytes": 5})
            self.assertFalse(observed["cachedir"])
            self.assertEqual(observed["allowed_extractors"], ["BiliBili"])
            for name in ("match_filter", "max_filesize", "progress_hooks"):
                self.assertNotIn(name, observed)


if __name__ == "__main__":
    unittest.main()
