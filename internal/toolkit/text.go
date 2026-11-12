package toolkit

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode"
	"unicode/utf8"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/parser/operator"
)

func init() {
	// Preserve the existing calculator's left-associative ^ without maintaining
	// a second parser. This process exposes only the bounded numeric evaluator.
	power := operator.Binary["^"]
	power.Associativity = operator.Left
	operator.Binary["^"] = power
}

var arithmetic = regexp.MustCompile(`^[0-9\s.()+*/^\-]+$`)
var numberLiteral = regexp.MustCompile(`\d+(?:\.\d*)?|\.\d+`)
var hasDigit = regexp.MustCompile(`[0-9]`)
var integer = regexp.MustCompile(`^-?[0-9]{1,13}$`)
var isoDate = regexp.MustCompile(`(?i)^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2}(?:\.\d{1,3})?)?(Z|[+-]\d{2}:\d{2})$`)
var shanghai, _ = time.LoadLocation("Asia/Shanghai")

func calculate(text string) (string, error) {
	s := strings.TrimSpace(text)
	s = strings.TrimPrefix(s, "=")
	s = strings.TrimSuffix(s, "=")
	s = strings.NewReplacer("×", "*", "÷", "/", "−", "-").Replace(s)
	invalid := errors.New("不是有效的有限算式。")
	if len(s) > 160 || !arithmetic.MatchString(s) || !hasDigit.MatchString(s) {
		return "", invalid
	}
	// Float-only numeric AST prevents integer overflow and language constructs
	// outside the bounded arithmetic character set. Builtins are disabled.
	s = numberLiteral.ReplaceAllStringFunc(s, func(value string) string {
		if !strings.Contains(value, ".") {
			return value + ".0"
		}
		return value
	})
	code, err := expr.Compile(s, expr.AsFloat64(), expr.DisableAllBuiltins(), expr.MaxNodes(128), expr.Optimize(false))
	if err != nil {
		return "", invalid
	}
	value, err := expr.Run(code, nil)
	if err != nil {
		return "", invalid
	}
	n, ok := value.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
		return "", invalid
	}
	rounded, err := strconv.ParseFloat(strconv.FormatFloat(n, 'g', 14, 64), 64)
	if err != nil {
		return "", invalid
	}
	if rounded == 0 {
		return "0", nil
	}
	if math.Abs(rounded) >= 1e21 || math.Abs(rounded) < 1e-6 {
		value := strconv.FormatFloat(rounded, 'e', -1, 64)
		value = regexp.MustCompile(`e([+-])0+(\d+)`).ReplaceAllString(value, "e${1}${2}")
		return value, nil
	}
	return strconv.FormatFloat(rounded, 'f', -1, 64), nil
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

func decodeBase64(text string) (string, error) {
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '\uFEFF' {
			return -1
		}
		return r
	}, text)
	compact = strings.NewReplacer("-", "+", "_", "/").Replace(compact)
	invalid := errors.New("Base64 无效或解码内容不是 UTF-8 文本。")
	if compact == "" {
		return "", invalid
	}
	enc := base64.StdEncoding.Strict()
	if !strings.Contains(compact, "=") {
		enc = base64.RawStdEncoding.Strict()
	}
	raw, err := enc.DecodeString(compact)
	if err != nil || !utf8.Valid(raw) {
		return "", invalid
	}
	return string(raw), nil
}

func encodeURL(text string) string {
	const digits = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.!~*'()", rune(c)) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(digits[c>>4])
			b.WriteByte(digits[c&15])
		}
	}
	return b.String()
}

func formatJSON(text string, compact bool) (string, error) {
	if !json.Valid([]byte(text)) {
		return "", errors.New("JSON 格式错误；不接受注释或尾逗号。")
	}
	depth, maxDepth, tokens := 0, 0, 0
	quoted, escaped := false, false
	for _, r := range text {
		if quoted {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == '"' {
				quoted = false
			}
			continue
		}
		if r == '"' {
			quoted = true
		}
		switch r {
		case '{', '[':
			depth++
			if depth > maxDepth {
				maxDepth = depth
			}
		case '}', ']':
			depth--
		case ',':
			tokens++
		}
		if depth > 64 || tokens > 50000 {
			return "", errors.New("JSON 结构过大或嵌套过深，请缩小内容后重试。")
		}
	}
	if !compact && len(text)+(tokens*2+2)*(maxDepth*2+2) > MaxResult {
		return "", errors.New("JSON 格式化结果可能过大，请缩小内容后重试。")
	}
	var b bytes.Buffer
	var err error
	if compact {
		err = json.Compact(&b, []byte(text))
	} else {
		err = json.Indent(&b, []byte(strings.TrimSpace(text)), "", "  ")
	}
	if err != nil {
		return "", errors.New("JSON 格式错误。")
	}
	if b.Len() > MaxResult {
		return "", errors.New("结果过大。")
	}
	return b.String(), nil
}

func timestamp(text string, now time.Time) (Result, error) {
	q := strings.TrimSpace(text)
	var date time.Time
	var err error
	if q == "" || strings.EqualFold(q, "now") || q == "现在" {
		date = now
	} else if integer.MatchString(q) {
		digits := len(strings.TrimPrefix(q, "-"))
		if digits > 10 && digits != 13 {
			return Result{}, errors.New("请输入秒时间戳或 13 位毫秒时间戳。")
		}
		var n int64
		n, err = strconv.ParseInt(q, 10, 64)
		if digits <= 10 {
			date = time.Unix(n, 0)
		} else {
			date = time.UnixMilli(n)
		}
	} else if isoDate.MatchString(q) {
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00"} {
			date, err = time.Parse(layout, q)
			if err == nil {
				break
			}
		}
	} else {
		err = errors.New("请输入秒/毫秒时间戳，或带 Z / +08:00 时区的 ISO 时间。")
	}
	if err != nil || date.Year() < 0 || date.Year() > 9999 {
		return Result{}, errors.New("日期无效、缺少时区或超出范围。")
	}
	utc := date.UTC().Format("2006-01-02T15:04:05.000Z")
	local := date.In(shanghai).Format("2006/1/2 15:04:05")
	fields := []Field{{"UTC", utc}, {"上海（Asia/Shanghai）", local}, {"Unix 秒", strconv.FormatInt(date.Unix(), 10)}, {"Unix 毫秒", strconv.FormatInt(date.UnixMilli(), 10)}}
	text = fmt.Sprintf("UTC：%s\n上海（Asia/Shanghai）：%s\nUnix 秒：%d\nUnix 毫秒：%d", utc, local, date.Unix(), date.UnixMilli())
	result := resultFor("timestamp", text)
	result.Type = "fields"
	result.Fields = fields
	return result, nil
}

func ExecuteText(ctx context.Context, name string, args Arguments) (Result, error) {
	if args.Clip != nil {
		return Result{}, errors.New("此工具不接受截取参数。")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	t, ok := findTool(name)
	if !ok || t.InputKind != "text" || t.Mode != "sync" {
		return Result{}, errors.New("不支持的文本工具。")
	}
	if len(args.Text) > MaxText*4 || utf8.RuneCountInString(args.Text) > MaxText {
		return Result{}, errors.New("文本不能超过 100 万字符。")
	}
	var text string
	var err error
	switch name {
	case "calculate":
		text, err = calculate(args.Text)
	case "base64-encode":
		text = base64.StdEncoding.EncodeToString([]byte(args.Text))
	case "base64-decode":
		text, err = decodeBase64(args.Text)
	case "url-encode":
		text = encodeURL(args.Text)
	case "url-decode":
		text, err = url.PathUnescape(args.Text)
		if err != nil || !utf8.ValidString(text) {
			err = errors.New("URL 编码无效，或包含不完整的 Unicode 字符。")
		}
	case "json-format", "json-compact":
		text, err = formatJSON(args.Text, name == "json-compact")
	case "uuid":
		text, err = newID()
	case "timestamp":
		return timestamp(args.Text, time.Now())
	}
	if err != nil {
		return Result{}, err
	}
	if len(text) > MaxResult {
		return Result{}, errors.New("结果过大，请缩小输入后重试。")
	}
	return resultFor(name, text), ctx.Err()
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected trailing input")
	}
	return nil
}
