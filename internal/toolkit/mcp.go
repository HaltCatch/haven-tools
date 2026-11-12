package toolkit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Service) mcpHandler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "haven-tools", Version: Version}, nil)
	for _, tool := range Catalog() {
		if tool.InputKind != "text" || tool.Mode != "sync" {
			continue
		}
		name := tool.Name
		server.AddTool(&mcp.Tool{Name: name, Title: tool.Title, Description: tool.Description, InputSchema: tool.InputSchema}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := decodeArguments(request.Params.Arguments)
			if err != nil {
				return toolError(err), nil
			}
			result, err := ExecuteText(ctx, name, args)
			if err != nil {
				return toolError(err), nil
			}
			return &mcp.CallToolResult{StructuredContent: result, Content: []mcp.Content{&mcp.TextContent{Text: result.Text}}}, nil
		})
	}
	for _, tool := range Catalog() {
		if tool.Mode != "async" || tool.InputKind != "text" {
			continue
		}
		name := tool.Name
		taskSchema := map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string", "maxLength": MaxText}, "requestId": map[string]any{"type": "string", "maxLength": 128}}, "required": []string{"text", "requestId"}, "additionalProperties": false}
		if name == "media-clip" {
			taskSchema["properties"].(map[string]any)["clip"] = clipSchema
			taskSchema["required"] = []string{"text", "requestId", "clip"}
		}
		server.AddTool(&mcp.Tool{Name: name, Title: tool.Title, Description: tool.Description + " 提交后台任务并返回编号/结果 URL；重试复用 requestId。", InputSchema: taskSchema}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args struct {
				Text      *string      `json:"text"`
				Clip      *ClipOptions `json:"clip,omitempty"`
				RequestID *string      `json:"requestId"`
			}
			if decodeStrict(request.Params.Arguments, &args) != nil || args.Text == nil || args.RequestID == nil {
				return toolError(errors.New("参数无效。")), nil
			}
			task, err := s.store.Submit(owner(ctx), *args.RequestID, name, Arguments{Text: *args.Text, Clip: args.Clip})
			if err != nil {
				return toolError(err), nil
			}
			return structured(task), nil
		})
	}
	idSchema := map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}, "additionalProperties": false}
	for _, name := range []string{"task-status", "task-result", "task-cancel"} {
		server.AddTool(&mcp.Tool{Name: name, Description: "按真实任务编号读取状态、结果或请求停止；只处理当前身份的任务。", InputSchema: idSchema}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args struct {
				ID string `json:"id"`
			}
			if decodeStrict(request.Params.Arguments, &args) != nil {
				return toolError(errors.New("参数无效。")), nil
			}
			var task Task
			var err error
			if name == "task-cancel" {
				task, err = s.store.Cancel(owner(ctx), args.ID)
			} else {
				task, err = s.store.Get(owner(ctx), args.ID)
			}
			if err != nil {
				return toolError(err), nil
			}
			if name == "task-status" {
				task.Result = nil
			}
			return structured(task), nil
		})
	}
	handler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 6_000_000)
		handler.ServeHTTP(w, r)
	})
}
func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}
func structured(value any) *mcp.CallToolResult {
	raw, _ := json.Marshal(value)
	return &mcp.CallToolResult{StructuredContent: value, Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
}
