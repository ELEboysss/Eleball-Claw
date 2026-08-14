package service

import (
	"context"
	"strings"
	"testing"
)

// TestBuildCreatorTools_NilDeps 依赖未装配时不产出工具（云端/未注入场景安全降级）。
func TestBuildCreatorTools_NilDeps(t *testing.T) {
	svc := &AgentService{}
	if tools := svc.buildCreatorTools(); len(tools) != 0 {
		t.Fatalf("依赖为 nil 时不应产出创造工具: %d", len(tools))
	}
}

// TestBuildCreatorTools_WithDeps 装配 moduleSvc+registry 后产出 3 个创造工具。
func TestBuildCreatorTools_WithDeps(t *testing.T) {
	svc := &AgentService{
		moduleSvc:   &ModuleService{},
		mcpRegistry: &MCPRegistryClient{BaseURL: "http://127.0.0.1:1"},
	}
	tools := svc.buildCreatorTools()
	if len(tools) != 3 {
		t.Fatalf("应产出 3 个创造工具: %d", len(tools))
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name] = true
	}
	for _, want := range []string{creatorToolSearchRegistry, creatorToolInstallMCP, creatorToolCreatePromptSkill} {
		if !names[want] {
			t.Fatalf("缺少创造工具 %s", want)
		}
	}
	// SearchMCPRegistry 只读（default 模式免审批），其余为写操作
	for _, tl := range tools {
		if tl.Name == creatorToolSearchRegistry && !tl.ReadOnly {
			t.Fatalf("SearchMCPRegistry 应为只读")
		}
		if tl.Name != creatorToolSearchRegistry && tl.ReadOnly {
			t.Fatalf("%s 不应为只读（需走审批闸）", tl.Name)
		}
	}
}

// TestNormalizeAgentMode 模式归一化：空/未知回落 standard，大小写不敏感。
func TestNormalizeAgentMode(t *testing.T) {
	cases := map[string]string{
		"":          AgentModeStandard,
		"standard":  AgentModeStandard,
		"creator":   AgentModeCreator,
		"Creator":   AgentModeCreator,
		" CREATOR ": AgentModeCreator,
		"banana":    AgentModeStandard,
	}
	for in, want := range cases {
		if got := NormalizeAgentMode(in); got != want {
			t.Fatalf("NormalizeAgentMode(%q)=%q, want %q", in, got, want)
		}
	}
}

// TestCreatorToolCreatePromptSkill 创造工具创建 prompt-only 秘技（含 slug 推导与 SKU 同步钩子）。
func TestCreatorToolCreatePromptSkill(t *testing.T) {
	t.Setenv("CLAW_MARKETPLACE_DIR", t.TempDir())
	svc := &AgentService{moduleSvc: &ModuleService{}}
	var syncedSkillID string
	svc.skillSyncFn = func(dir, skillID, creatorID, creatorName string) (int, int, int) {
		syncedSkillID = skillID
		return 1, 0, 0
	}
	tools := svc.buildCreatorTools()
	var create *Tool
	for _, tl := range tools {
		if tl.Name == creatorToolCreatePromptSkill {
			create = tl
		}
	}
	if create == nil {
		t.Fatal("缺少 CreatePromptSkill 工具")
	}
	out, err := create.Func(context.Background(), map[string]interface{}{
		"name":        "周报助手",
		"skill_id":    "weekly-report",
		"description": "需要写工作周报时使用",
		"body":        "你是一位周报撰写助手……",
	}, &ToolEnv{UserID: "u1"})
	if err != nil {
		t.Fatalf("创建秘技不应报错: %v", err)
	}
	skillID, _ := out["skill_id"].(string)
	if skillID != "weekly-report" {
		t.Fatalf("skill_id 不对: %q", skillID)
	}
	if out["synced"] != true {
		t.Fatalf("SKU 同步应成功: %v", out)
	}
	if syncedSkillID != skillID {
		t.Fatalf("同步钩子收到 skill_id=%q, want %q", syncedSkillID, skillID)
	}
}

// TestCreatorToolInstallMCP_StdioNeedsCommand stdio 未装配协议或缺 command 时返回清晰错误（不 panic）。
func TestCreatorToolInstallMCP_StdioNeedsCommand(t *testing.T) {
	svc := &AgentService{moduleSvc: &ModuleService{}}
	// 未装配 mcpStdio：协议错误优先于参数校验
	_, err := svc.installMCPFromCreator(context.Background(), map[string]interface{}{
		"name": "x",
	}, &ToolEnv{})
	if err == nil || !strings.Contains(err.Error(), "stdio") {
		t.Fatalf("未装配 stdio 协议应报错: %v", err)
	}
	// 装配协议后缺 command：参数校验报错
	svc.mcpStdio = &MCPStdioProtocol{}
	_, err = svc.installMCPFromCreator(context.Background(), map[string]interface{}{
		"name": "x",
	}, &ToolEnv{})
	if err == nil || !strings.Contains(err.Error(), "command") {
		t.Fatalf("stdio 缺 command 应报错: %v", err)
	}
}

// TestCreatorToolSearchRegistry_UpstreamFail 注册表不可达时返回错误（不 panic）。
func TestCreatorToolSearchRegistry_UpstreamFail(t *testing.T) {
	svc := &AgentService{mcpRegistry: &MCPRegistryClient{BaseURL: "http://127.0.0.1:1"}}
	tools := svc.buildCreatorTools()
	if len(tools) != 1 || tools[0].Name != creatorToolSearchRegistry {
		t.Fatalf("仅 registry 装配时应只有搜索工具: %v", tools)
	}
	_, err := tools[0].Func(context.Background(), map[string]interface{}{"query": "fs"}, &ToolEnv{})
	if err == nil {
		t.Fatal("上游不可达应返回错误")
	}
}
