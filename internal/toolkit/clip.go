package toolkit

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
)

type ClipOptions struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Mode  string  `json:"mode"`
}

var clipSchema = map[string]any{"type": "object", "properties": map[string]any{"start": map[string]any{"type": "number", "minimum": 0}, "end": map[string]any{"type": "number", "exclusiveMinimum": 0}, "mode": map[string]any{"type": "string", "enum": []string{"copy", "exact", "audio"}}}, "required": []string{"start", "end", "mode"}, "additionalProperties": false}

func validateClip(c *ClipOptions) error {
	if c == nil || math.IsNaN(c.Start) || math.IsNaN(c.End) || math.IsInf(c.Start, 0) || math.IsInf(c.End, 0) || c.Start < 0 || c.End <= c.Start || c.End > 31536000 {
		return errors.New("截取起止秒数无效。")
	}
	if c.Mode != "copy" && c.Mode != "exact" && c.Mode != "audio" {
		return errors.New("截取方式无效。")
	}
	return nil
}
func clipMedia(ctx context.Context, a Arguments, j Job, input string) (Result, error) {
	if err := validateClip(a.Clip); err != nil {
		return Result{}, err
	}
	if err := j.Report("FFmpeg 截取所选时间段"); err != nil {
		return Result{}, err
	}
	c := a.Clip
	before := []string{"-nostdin", "-v", "error", "-ss", strconv.FormatFloat(c.Start, 'f', 6, 64)}
	after := []string{"-t", strconv.FormatFloat(c.End-c.Start, 'f', 6, 64), "-map", "0:v:0?", "-map", "0:a:0?", "-sn", "-dn", "-threads", "1"}
	name, ct := "clip.mkv", "video/x-matroska"
	switch c.Mode {
	case "copy":
		after = append(after, "-c", "copy", "-avoid_negative_ts", "make_zero")
	case "exact":
		name, ct = "clip.mp4", "video/mp4"
		after = append(after, "-c:v", "libx264", "-preset", "fast", "-crf", "20", "-pix_fmt", "yuv420p", "-vf", "pad=ceil(iw/2)*2:ceil(ih/2)*2", "-c:a", "aac", "-b:a", "160k", "-movflags", "+faststart")
	case "audio":
		name, ct = "clip.m4a", "audio/mp4"
		after = append(after, "-vn", "-c:a", "aac", "-b:a", "160k")
	}
	after = append(after, "-y", filepath.Join(j.Dir, name))
	if err := runMediaCommand(ctx, input, "ffmpeg", before, after, nil); err != nil {
		if errors.Is(err, errDiskSpace) {
			return Result{}, err
		}
		return Result{}, errors.New("截取失败：请检查文件格式、所选音轨和时间范围。")
	}
	// Reject an out-of-range interval that generated only a container header.
	duration, err := mediaDuration(ctx, filepath.Join(j.Dir, name))
	if err != nil {
		return Result{}, err
	}
	if err := publish(j, name, "下载截取结果", name, ct); err != nil {
		return Result{}, err
	}
	os.Remove(input)
	result := resultFor("media-clip", fmt.Sprintf("请求截取 %.3f–%.3f 秒；结果时长 %.3f 秒。", c.Start, c.End, duration.Seconds()))
	if c.Mode == "copy" {
		result.Text += "快速模式保留编码，实际起点受关键帧影响；需要准确边界请用精确模式。"
	}
	result.Links = []Link{{"下载截取结果", j.URL + "/files/" + name}}
	result.Description = "临时文件保留 24 小时，到期或服务重建后清理。"
	return result, nil
}
