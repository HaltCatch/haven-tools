"""Exercise actual native converters in the runtime image, without network/model."""
import importlib.util
import io
from pathlib import Path
import shutil
import tempfile
import unittest

try:
    from PIL import Image, ImageDraw, ImageFont
except ImportError:
    Image = None


@unittest.skipUnless(Image, "Pillow runtime dependency required")
class Processing(unittest.TestCase):
    def setUp(self):
        spec = importlib.util.spec_from_file_location("processor", Path(__file__).with_name("process.py"))
        self.module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.module)
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def test_formats_dimensions_and_metadata(self):
        source = self.root / "source.png"
        im = Image.new("RGBA", (2400, 1200), (255, 0, 0, 90))
        im.save(source)
        for operation in ("image-compress", "image-resize", "image-png", "image-jpeg", "image-webp"):
            result = self.module.process(operation, source, self.root)
            with Image.open(self.root / result["file"]) as converted:
                self.assertEqual(converted.size, (1920, 960) if operation == "image-resize" else im.size)
                self.assertFalse(converted.getexif())
                if operation == "image-png":
                    self.assertEqual(converted.getpixel((0, 0))[3], 90)

    def test_invalid_file_fails(self):
        source = self.root / "broken"; source.write_text("not an image")
        with self.assertRaises(Exception):
            self.module.process("image-png", source, self.root)

if __name__ == "__main__":
    unittest.main()
