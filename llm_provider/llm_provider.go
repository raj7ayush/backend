package llmprovider

import (
	"fmt"
	"os"
	"strings"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

const (
	defaultBaseURL = "https://integrate.api.nvidia.com/v1"
	defaultModel   = "qwen/qwen3-coder-480b-a35b-instruct"
)

// NewGroqLLM constructs an OpenAI-compatible LLM using configuration provided via
// environment variables. The following variables are respected:
//   - LLM_API_TOKEN (required)
//   - LLM_BASE_URL (optional, defaults to https://integrate.api.nvidia.com/v1)
//   - LLM_MODEL (optional, defaults to qwen/qwen3-coder-480b-a35b-instruct)
func NewGroqLLM() (llms.Model, error) {
	token := strings.TrimSpace(os.Getenv("LLM_API_TOKEN"))
	if token == "" {
		return nil, fmt.Errorf("missing LLM_API_TOKEN environment variable")
	}

	baseURL := strings.TrimSpace(os.Getenv("LLM_BASE_URL"))
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	model := strings.TrimSpace(os.Getenv("LLM_MODEL"))
	if model == "" {
		model = defaultModel
	}

	return openai.New(
		openai.WithToken(token),
		openai.WithBaseURL(baseURL),
		openai.WithModel(model),
	)
}
