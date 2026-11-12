package toolkit

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

const ProtocolVersion = 1
const MaxText = 1_000_000
const MaxResult = 8_000_000

type Icon struct {
	Name  string   `json:"name"`
	Paths []string `json:"paths"`
}
type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
type Link struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

// The presentation contract is deliberately finite, not a layout language.
type Result struct {
	Type         string  `json:"type"`
	Title        string  `json:"title"`
	Text         string  `json:"text"`
	Description  string  `json:"description,omitempty"`
	Icon         Icon    `json:"icon"`
	Fields       []Field `json:"fields,omitempty"`
	Links        []Link  `json:"links,omitempty"`
	CopyOnSelect bool    `json:"copyOnSelect,omitempty"`
}
type Arguments struct {
	Text string       `json:"text"`
	Clip *ClipOptions `json:"clip,omitempty"`
}
type Action struct {
	Editor     bool       `json:"editor,omitempty"`
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	Detail     string     `json:"detail"`
	Icon       Icon       `json:"icon"`
	Score      int        `json:"score"`
	HandlerID  string     `json:"handlerId,omitempty"`
	Arguments  *Arguments `json:"arguments,omitempty"`
	InputKind  string     `json:"inputKind,omitempty"`
	UploadPath string     `json:"uploadPath,omitempty"`
	Value      string     `json:"value"`
}
type Tool struct {
	Editor      bool           `json:"editor,omitempty"`
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Category    string         `json:"category"`
	Icon        Icon           `json:"icon"`
	Mode        string         `json:"mode"`
	InputKind   string         `json:"inputKind"`
	Example     string         `json:"example"`
	InputSchema map[string]any `json:"inputSchema"`
}
type Handler func(context.Context, Arguments) (Result, error)

var ErrBusy = errors.New("工具服务繁忙，请稍后重试。")

func strictJSON(data []byte, target any) error {
	return decodeStrict(data, target)
}

func decodeArguments(data json.RawMessage) (Arguments, error) {
	var args Arguments
	if err := strictJSON(data, &args); err != nil {
		return args, errors.New("工具参数无效。")
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(data, &keys) != nil || (len(keys) != 1 && !(len(keys) == 2 && keys["clip"] != nil)) || keys["text"] == nil || utf8.RuneCountInString(args.Text) > MaxText || string(keys["text"]) == "null" || string(keys["clip"]) == "null" {
		return args, errors.New("请输入有效的文本参数。")
	}
	return args, nil
}
