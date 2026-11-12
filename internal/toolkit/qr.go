package toolkit

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

func DecodeQR(ctx context.Context, data []byte) (Result, error) {
	return decodeQR(ctx, bytes.NewReader(data))
}

func decodeQR(ctx context.Context, input io.ReadSeeker) (Result, error) {
	config, _, err := image.DecodeConfig(input)
	if err != nil {
		return Result{}, errors.New("无法读取图片，请使用 PNG、JPEG、WebP、GIF 或 BMP。")
	}
	if config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 32_000_000 {
		return Result{}, errors.New("图片超过 3200 万像素，请裁剪后重试。")
	}
	// PNG can encode 16-bit RGBA in a tiny compressed body. Bound decoded
	// storage before allocation, in addition to the pixel limit.
	bytesPerPixel := int64(4)
	if config.ColorModel == color.RGBA64Model || config.ColorModel == color.NRGBA64Model {
		bytesPerPixel = 8
	}
	if int64(config.Width)*int64(config.Height)*bytesPerPixel > 128*1024*1024 {
		return Result{}, errors.New("图片解码需要过多内存，请裁剪或转换成 8 位 PNG/JPEG 后重试。")
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return Result{}, err
	}
	img, _, err := image.Decode(input)
	if err != nil {
		return Result{}, errors.New("图片内容无效。")
	}
	longest := max(config.Width, config.Height)
	for _, limit := range []int{min(longest, 2000), min(longest, 4096)} {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		ratio := float64(limit) / float64(longest)
		w := max(1, int(math.Round(float64(config.Width)*ratio)))
		h := max(1, int(math.Round(float64(config.Height)*ratio)))
		scaled := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(scaled, scaled.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), img, img.Bounds(), draw.Over, nil)
		if limit == min(longest, 4096) {
			img = nil // The final scaled attempt no longer needs the full original.
		}
		source := gozxing.NewLuminanceSourceFromImage(scaled)
		for _, lum := range []gozxing.LuminanceSource{source, source.Invert()} {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			bitmap, e := gozxing.NewBinaryBitmap(gozxing.NewHybridBinarizer(lum))
			if e != nil {
				continue
			}
			decoded, e := qrcode.NewQRCodeReader().Decode(bitmap, map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_TRY_HARDER: true})
			if e == nil {
				result := resultFor("qr-decode", decoded.GetText())
				result.Title = "二维码内容"
				return result, nil
			}
		}
		if limit == longest {
			break
		}
	}
	return Result{}, errors.New("没有识别到二维码。请保留完整边框，裁剪二维码区域或换一张更清晰的图片。")
}
