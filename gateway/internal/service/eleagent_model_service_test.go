package service

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupEleAgentModelService 搭建带内存数据库与加密 Master Key 的模型配置服务
func setupEleAgentModelService(t *testing.T) *EleAgentModelService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.EleAgentModelConfig{}))

	repo := repository.NewEleAgentModelRepo(db)
	masterKey := hex.EncodeToString(make([]byte, 32))
	svc, err := NewEleAgentModelService(repo, masterKey)
	require.NoError(t, err)
	return svc
}

func seedEleAgentConfigs(t *testing.T, svc *EleAgentModelService) {
	t.Helper()
	_, err := svc.CreateConfig(EleAgentModelConfigInput{
		Provider:   "kimi",
		Protocol:   string(model.EleAgentUpstreamOpenAICompatible),
		ModelName:  "k3",
		BaseURL:    "https://api.kimi.com/coding/v1",
		APIKey:     "sk-kimi-test",
		SupportsChat:  true,
		SupportsTools: true,
	})
	require.NoError(t, err)
	_, err = svc.CreateConfig(EleAgentModelConfigInput{
		Provider:           "volcengine",
		Protocol:           string(model.EleAgentUpstreamSeedream),
		ModelName:          "doubao-seedream-4-0-250828",
		BaseURL:            "https://ark.cn-beijing.volces.com/api/v3",
		APIKey:             "ark-test-key",
		SupportsImage:      true,
		SupportsImageInput: true,
	})
	require.NoError(t, err)
}

func TestExportConfigsWithoutKeys(t *testing.T) {
	svc := setupEleAgentModelService(t)
	seedEleAgentConfigs(t, svc)

	data, err := svc.ExportConfigs(false)
	require.NoError(t, err)
	assert.Equal(t, 1, data.Version)
	assert.False(t, data.IncludeKeys)
	// 字段说明与导入规则随文件带出，供人工编辑参考
	assert.NotEmpty(t, data.Usage)
	assert.Contains(t, data.FieldNotes, "video_max_duration")
	assert.Contains(t, data.FieldNotes, "protocol")
	require.Len(t, data.Items, 2)
	for _, item := range data.Items {
		assert.Empty(t, item.APIKey, "不含密钥导出时不应携带 api_key")
		assert.NotEmpty(t, item.Provider)
		assert.NotEmpty(t, item.Protocol)
	}
}

func TestExportConfigsWithKeys(t *testing.T) {
	svc := setupEleAgentModelService(t)
	seedEleAgentConfigs(t, svc)

	data, err := svc.ExportConfigs(true)
	require.NoError(t, err)
	assert.True(t, data.IncludeKeys)
	require.Len(t, data.Items, 2)

	keys := map[string]string{}
	for _, item := range data.Items {
		keys[item.Provider+"/"+item.ModelName] = item.APIKey
	}
	assert.Equal(t, "sk-kimi-test", keys["kimi/k3"])
	assert.Equal(t, "ark-test-key", keys["volcengine/doubao-seedream-4-0-250828"])
}

func TestImportConfigsCreate(t *testing.T) {
	svc := setupEleAgentModelService(t)

	result, err := svc.ImportConfigs([]EleAgentModelExportItem{
		{
			Provider:      "kimi",
			Protocol:      "openai_compatible",
			ModelName:     "k3",
			BaseURL:       "https://api.kimi.com/coding/v1",
			APIKey:        "sk-kimi-test",
			SupportsChat:  true,
			SupportsTools: true,
		},
		{
			// 协议缺省应回退 openai_compatible
			Provider:     "qwen",
			ModelName:    "Qwen/Qwen3-8B",
			BaseURL:      "https://api.siliconflow.cn/v1",
			APIKey:       "sk-qwen",
			SupportsChat: true,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Created)
	assert.Equal(t, 0, result.Updated)
	assert.Empty(t, result.Failed)

	// 凭据可正常解密使用
	cred, err := svc.GetCredential("kimi", "k3")
	require.NoError(t, err)
	assert.Equal(t, "sk-kimi-test", cred.APIKey)
	assert.Equal(t, "openai_compatible", cred.Protocol)

	cred2, err := svc.GetCredential("qwen", "Qwen/Qwen3-8B")
	require.NoError(t, err)
	assert.Equal(t, "openai_compatible", cred2.Protocol)
}

func TestImportConfigsUpdateKeepsKey(t *testing.T) {
	svc := setupEleAgentModelService(t)
	seedEleAgentConfigs(t, svc)

	// 重新导入同 provider/model，修改展示名与单价，不提供 api_key → 保留原 Key
	// （代码内构造视为全字段提供，需带上有效能力值）
	displayName := "Kimi K3 旗舰"
	result, err := svc.ImportConfigs([]EleAgentModelExportItem{
		{
			Provider:        "kimi",
			Protocol:        "openai_compatible",
			ModelName:       "k3",
			DisplayName:     displayName,
			BaseURL:         "https://api.kimi.com/coding/v1",
			PricePerCall:    99,
			SupportsChat:    true,
			SupportsTools:   true,
			SupportsVision:  true,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, result.Created)
	assert.Equal(t, 1, result.Updated)
	assert.Empty(t, result.Failed)

	cred, err := svc.GetCredential("kimi", "k3")
	require.NoError(t, err)
	assert.Equal(t, "sk-kimi-test", cred.APIKey, "未提供 api_key 时应保留原 Key")

	items, _, err := svc.ListConfigs("kimi", 1, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, displayName, items[0].DisplayName)
	assert.Equal(t, int64(99), items[0].PricePerCall)
	assert.True(t, items[0].SupportsVision)
}

func TestImportConfigsUpdateRotatesKey(t *testing.T) {
	svc := setupEleAgentModelService(t)
	seedEleAgentConfigs(t, svc)

	result, err := svc.ImportConfigs([]EleAgentModelExportItem{
		{
			// 代码内构造视为全字段提供（全量更新），需带上有效能力值
			Provider:     "kimi",
			ModelName:    "k3",
			BaseURL:      "https://api.kimi.com/coding/v1",
			APIKey:       "sk-kimi-new",
			SupportsChat: true,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Updated)

	cred, err := svc.GetCredential("kimi", "k3")
	require.NoError(t, err)
	assert.Equal(t, "sk-kimi-new", cred.APIKey, "提供 api_key 时应轮换 Key")
}

func TestImportConfigsFailures(t *testing.T) {
	svc := setupEleAgentModelService(t)
	seedEleAgentConfigs(t, svc)

	result, err := svc.ImportConfigs([]EleAgentModelExportItem{
		// 新建缺少 api_key
		{Provider: "openai", ModelName: "gpt-4o", BaseURL: "https://api.openai.com/v1", SupportsChat: true},
		// 视觉协议与能力不一致（seedream 不能配视频）
		{Provider: "volcengine", Protocol: "seedream", ModelName: "m1", BaseURL: "https://x", APIKey: "k", SupportsVideo: true},
		// 对话协议不允许声明图片生成能力
		{Provider: "openai", Protocol: "openai_compatible", ModelName: "gpt-image", BaseURL: "https://x", APIKey: "k", SupportsChat: true, SupportsImage: true},
		// 对话/图片/视频三项能力全缺
		{Provider: "openai", ModelName: "gpt-4o-nano", BaseURL: "https://x", APIKey: "k"},
		// 单价为负
		{Provider: "openai", ModelName: "gpt-4o-mini", BaseURL: "https://api.openai.com/v1", APIKey: "k", SupportsChat: true, PricePerCall: -1},
		// 文件内重复
		{Provider: "dup", ModelName: "m", BaseURL: "https://x", APIKey: "k", SupportsChat: true},
		{Provider: "dup", ModelName: "m", BaseURL: "https://x", APIKey: "k", SupportsChat: true},
		// 缺少必填字段
		{Provider: "", ModelName: "m", BaseURL: "https://x", APIKey: "k", SupportsChat: true},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Created, "重复的 dup/m 只有第一条应创建成功")
	assert.Equal(t, 0, result.Updated)
	require.Len(t, result.Failed, 7)

	assert.Contains(t, result.Failed[0].Error, "api_key")
	assert.Contains(t, result.Failed[1].Error, "视频")
	assert.Contains(t, result.Failed[2].Error, "视觉生成模型协议必须")
	assert.Contains(t, result.Failed[3].Error, "至少需要支持")
	assert.Contains(t, result.Failed[4].Error, "负数")
	assert.Contains(t, result.Failed[5].Error, "重复")
	assert.Contains(t, result.Failed[6].Error, "不能为空")
	// 失败行携带原始下标
	assert.Equal(t, 0, result.Failed[0].Index)
	assert.Equal(t, 6, result.Failed[5].Index)
}

func TestImportConfigsExportRoundTrip(t *testing.T) {
	// 环境 A 导出（含密钥）→ 环境 B 导入 → 配置一致可用
	svcA := setupEleAgentModelService(t)
	seedEleAgentConfigs(t, svcA)
	exported, err := svcA.ExportConfigs(true)
	require.NoError(t, err)

	svcB := setupEleAgentModelService(t)
	result, err := svcB.ImportConfigs(exported.Items)
	require.NoError(t, err)
	assert.Equal(t, 2, result.Created)
	assert.Empty(t, result.Failed)

	cred, err := svcB.GetCredential("volcengine", "doubao-seedream-4-0-250828")
	require.NoError(t, err)
	assert.Equal(t, "ark-test-key", cred.APIKey)
	assert.Equal(t, "seedream", cred.Protocol)

	// supports_chat 随导出/导入往返保留：对话模型 true，纯视觉模型 false
	kimiItems, _, err := svcB.ListConfigs("kimi", 1, 10)
	require.NoError(t, err)
	require.Len(t, kimiItems, 1)
	assert.True(t, kimiItems[0].SupportsChat)
	volcItems, _, err := svcB.ListConfigs("volcengine", 1, 10)
	require.NoError(t, err)
	require.Len(t, volcItems, 1)
	assert.False(t, volcItems[0].SupportsChat)
}

// importJSONItems 以 JSON 原文构造导入项（与 HTTP 入口一致，触发字段存在性跟踪）
func importJSONItems(t *testing.T, raw string) []EleAgentModelExportItem {
	t.Helper()
	var items []EleAgentModelExportItem
	require.NoError(t, json.Unmarshal([]byte(raw), &items))
	return items
}

// TestImportConfigsPartialJSONUpdate 手工精简 JSON 只覆盖出现的字段，未出现字段保持原值
func TestImportConfigsPartialJSONUpdate(t *testing.T) {
	svc := setupEleAgentModelService(t)
	seedEleAgentConfigs(t, svc)

	// 精简文件：只改 k3 的输出单价、seedream 的次数单价
	items := importJSONItems(t, `[
		{"provider": "kimi", "model_name": "k3", "price_per_call": 50},
		{"provider": "volcengine", "model_name": "doubao-seedream-4-0-250828", "price_per_generation": 7}
	]`)
	result, err := svc.ImportConfigs(items)
	require.NoError(t, err)
	assert.Equal(t, 2, result.Updated)
	assert.Equal(t, 0, result.Created)
	assert.Empty(t, result.Failed)

	// k3：价格被更新，其余字段（协议/能力/展示名/base_url/Key）全部保持原值
	cred, err := svc.GetCredential("kimi", "k3")
	require.NoError(t, err)
	assert.Equal(t, "openai_compatible", cred.Protocol)
	assert.Equal(t, "https://api.kimi.com/coding/v1", cred.BaseURL)
	assert.Equal(t, "sk-kimi-test", cred.APIKey)
	items3, _, err := svc.ListConfigs("kimi", 1, 10)
	require.NoError(t, err)
	require.Len(t, items3, 1)
	assert.Equal(t, int64(50), items3[0].PricePerCall)
	assert.True(t, items3[0].SupportsTools, "未出现的 supports_tools 应保持 true")
	assert.True(t, items3[0].SupportsChat, "未出现的 supports_chat 应保持 true")

	// seedream：协议仍是 seedream、图片能力保持 true（若被默认值洗坏此处会暴露）
	cred2, err := svc.GetCredential("volcengine", "doubao-seedream-4-0-250828")
	require.NoError(t, err)
	assert.Equal(t, "seedream", cred2.Protocol)
	imgCap, vidCap := svc.GetModelMediaCapabilities("volcengine", "doubao-seedream-4-0-250828")
	assert.True(t, imgCap, "未出现的 supports_image 应保持 true")
	assert.False(t, vidCap)
}

// TestImportConfigsPartialJSONConflict 部分更新与现有值组合出不一致状态时按行拒绝
func TestImportConfigsPartialJSONConflict(t *testing.T) {
	svc := setupEleAgentModelService(t)
	seedEleAgentConfigs(t, svc)

	items := importJSONItems(t, `[
		{"provider": "volcengine", "model_name": "doubao-seedream-4-0-250828", "protocol": "agnes_video"},
		{"provider": "kimi", "model_name": "k3", "base_url": ""},
		{"provider": "kimi", "model_name": "k3", "video_min_duration": 10, "video_max_duration": 5}
	]`)
	result, err := svc.ImportConfigs(items)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Updated)
	require.Len(t, result.Failed, 3)
	// agnes_video + 现有 supports_image=true → 协议能力冲突
	assert.Contains(t, result.Failed[0].Error, "视频生成协议不支持图片生成")
	// base_url 出现但为空
	assert.Contains(t, result.Failed[1].Error, "base_url")
	// 时长范围冲突（按有效值校验）
	assert.Contains(t, result.Failed[2].Error, "最小时长")

	// 冲突行未产生任何写入
	cred, err := svc.GetCredential("volcengine", "doubao-seedream-4-0-250828")
	require.NoError(t, err)
	assert.Equal(t, "seedream", cred.Protocol)
}

// TestCreateConfigRequiresAnyCapability 对话/图片/视频三项能力至少需要开启一项
func TestCreateConfigRequiresAnyCapability(t *testing.T) {
	svc := setupEleAgentModelService(t)

	// 三项能力全缺 → 拒绝
	_, err := svc.CreateConfig(EleAgentModelConfigInput{
		Provider:  "openai",
		ModelName: "gpt-4o-mini",
		BaseURL:   "https://api.openai.com/v1",
		APIKey:    "sk-test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "至少需要支持")

	// 仅对话能力 → 允许
	_, err = svc.CreateConfig(EleAgentModelConfigInput{
		Provider:     "openai",
		ModelName:    "gpt-4o-mini",
		BaseURL:      "https://api.openai.com/v1",
		APIKey:       "sk-test",
		SupportsChat: true,
	})
	require.NoError(t, err)

	// 对话协议 + 图片生成能力 → 协议能力不一致，拒绝
	_, err = svc.CreateConfig(EleAgentModelConfigInput{
		Provider:      "openai",
		Protocol:      "openai_compatible",
		ModelName:    "gpt-image-1",
		BaseURL:      "https://api.openai.com/v1",
		APIKey:       "sk-test",
		SupportsChat:  true,
		SupportsImage: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "视觉生成模型协议必须")
}

// TestUpdateConfigRequiresAnyCapability 更新后有效能力不满足至少一项时拒绝
func TestUpdateConfigRequiresAnyCapability(t *testing.T) {
	svc := setupEleAgentModelService(t)
	seedEleAgentConfigs(t, svc)

	items, _, err := svc.ListConfigs("kimi", 1, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)

	// kimi/k3 原本仅对话能力，关闭 supports_chat 后无任何能力 → 拒绝
	off := false
	_, err = svc.UpdateConfig(items[0].ID, EleAgentModelConfigPatch{SupportsChat: &off})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "至少需要支持")
}

// TestGetModelChatSupport 对话能力查询：对话模型 true，纯视觉模型 false
func TestGetModelChatSupport(t *testing.T) {
	svc := setupEleAgentModelService(t)
	seedEleAgentConfigs(t, svc)

	assert.True(t, svc.GetModelChatSupport("kimi", "k3"))
	assert.False(t, svc.GetModelChatSupport("volcengine", "doubao-seedream-4-0-250828"))
	assert.False(t, svc.GetModelChatSupport("unknown", "m"), "未配置模型返回 false")
}
