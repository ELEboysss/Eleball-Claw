package llm

import "context"

// EmbeddingClient 向量嵌入客户端接口（AR-09 记忆升级）。
//
// 调用方据此将记忆检索从 LIKE 升级为向量余弦相似度；实现可为 nil，
// 此时 TeamMemoryService 自动降级回 LIKE 检索（claw 本地无 embedding 服务时）。
type EmbeddingClient interface {
	// Embed 返回输入文本对应的向量列表，顺序与 inputs 一致。
	// model 为 embedding 模型名（EleAgent 模型中心 OpenAI 兼容 /embeddings）。
	Embed(ctx context.Context, model string, inputs []string) ([][]float32, error)
}

// EmbeddingRequest OpenAI 兼容 /embeddings 请求体
type EmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// EmbeddingResponse OpenAI 兼容 /embeddings 响应体
type EmbeddingResponse struct {
	Data  []EmbeddingItem `json:"data"`
	Usage *Usage          `json:"usage,omitempty"`
}

// EmbeddingItem 单条嵌入结果
type EmbeddingItem struct {
	Embedding []float32 `json:"embedding"`
}
