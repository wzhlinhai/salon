package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	openai "github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/openai/openai-go/v2/packages/param"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// DefaultBaseURL 是 DeepSeek 的 OpenAI 兼容接口地址。
const DefaultBaseURL = "https://api.deepseek.com"

// DefaultModel 是 DeepSeek V4.1 Flash 的模型名。
const DefaultModel = "deepseek-flash"

// OpenAIConfig 是 OpenAI 兼容客户端的配置。
type OpenAIConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

// OpenAI 用 openai-go 访问 OpenAI 兼容的 Chat Completions + tools 接口。
type OpenAI struct {
	client openai.Client
	model  string
}

// NewOpenAIModel 返回一个 OpenAI 兼容模型实现；未填的地址和模型名使用 DeepSeek 默认值。
func NewOpenAIModel(cfg OpenAIConfig) *OpenAI {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	opts := []option.RequestOption{
		option.WithBaseURL(cfg.BaseURL),
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(0),
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}
	return &OpenAI{client: openai.NewClient(opts...), model: cfg.Model}
}

// Chat 发送一次 Chat Completions 请求并解析文本与工具调用。
func (o *OpenAI) Chat(ctx context.Context, messages []Message, tools []ops.Tool) (Message, error) {
	params := openai.ChatCompletionNewParams{
		Messages: toOpenAIMessages(messages),
		Model:    o.model,
	}
	if len(tools) > 0 {
		params.Tools = make([]openai.ChatCompletionToolUnionParam, 0, len(tools))
		for _, tool := range tools {
			params.Tools = append(params.Tools, openai.ChatCompletionToolUnionParam{
				OfFunction: &openai.ChatCompletionFunctionToolParam{
					Function: openai.FunctionDefinitionParam{
						Name:        tool.Name,
						Description: openai.String(tool.Description),
						Parameters:  openai.FunctionParameters(tool.Parameters),
					},
				},
			})
		}
	}
	completion, err := o.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return Message{}, modelError(err)
	}
	if len(completion.Choices) == 0 {
		return Message{}, &ModelError{Err: ErrMalformedResponse}
	}
	choice := completion.Choices[0].Message
	out := Message{Role: RoleAssistant, Content: choice.Content}
	for _, call := range choice.ToolCalls {
		if call.Type != "function" {
			return Message{}, &ModelError{Err: ErrMalformedResponse}
		}
		function := call.AsFunction().Function
		arguments := function.Arguments
		if arguments == "" {
			arguments = "{}"
		}
		if !json.Valid([]byte(arguments)) {
			return Message{}, &ModelError{Err: ErrMalformedResponse}
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: call.ID, Name: function.Name, Arguments: arguments})
	}
	return out, nil
}

func toOpenAIMessages(messages []Message) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case RoleSystem:
			out = append(out, openai.SystemMessage(message.Content))
		case RoleUser:
			out = append(out, openai.UserMessage(message.Content))
		case RoleAssistant:
			assistant := openai.ChatCompletionAssistantMessageParam{}
			if message.Content != "" {
				assistant.Content.OfString = param.NewOpt(message.Content)
			}
			for _, call := range message.ToolCalls {
				assistant.ToolCalls = append(assistant.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: call.ID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      call.Name,
							Arguments: call.Arguments,
						},
					},
				})
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant})
		case RoleTool:
			out = append(out, openai.ToolMessage(message.Content, message.ToolCallID))
		}
	}
	return out
}

func modelError(err error) error {
	var apiErr *openai.Error
	status := 0
	if errors.As(err, &apiErr) {
		status = apiErr.StatusCode
	}
	return &ModelError{Status: status, Err: err}
}
