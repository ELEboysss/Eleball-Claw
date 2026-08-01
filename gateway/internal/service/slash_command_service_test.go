package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupSlashService(t *testing.T) (*SlashCommandService, *repository.AgentRepo, *gorm.DB, string) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.AgentItem{},
		&model.AgentPurchase{},
		&model.AgentUserTool{},
	))
	agentRepo := repository.NewAgentRepo(db)

	tmpDir := t.TempDir()
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0755))
	sandbox := NewFileSandbox(filepath.Join(tmpDir, "sandbox"), "")

	return NewSlashCommandService(agentRepo, promptsDir, sandbox), agentRepo, db, tmpDir
}

func seedAgent(t *testing.T, db *gorm.DB, id, name, desc string, status model.AgentStatus) {
	require.NoError(t, db.Create(&model.AgentItem{
		ID:           id,
		Name:         name,
		Description:  desc,
		Status:       status,
		ManifestJSON: `{"name":"` + name + `","driver":"builtin"}`,
	}).Error)
}

func seedPurchaseAndTool(t *testing.T, db *gorm.DB, userID, agentID string) {
	require.NoError(t, db.Create(&model.AgentPurchase{
		ID:      uuid.New().String(),
		AgentID: agentID,
		BuyerID: userID,
	}).Error)
	require.NoError(t, db.Create(&model.AgentUserTool{
		ID:      uuid.New().String(),
		UserID:  userID,
		AgentID: agentID,
		Active:  true,
	}).Error)
}

func TestSlashCommandService_ListCommands_Builtin(t *testing.T) {
	svc, _, _, _ := setupSlashService(t)
	resp, err := svc.ListCommands("u1")
	require.NoError(t, err)
	require.Len(t, resp.Categories, 3)
	assert.Equal(t, SlashCategoryBuiltin, resp.Categories[0].Name)
	assert.GreaterOrEqual(t, len(resp.Categories[0].Commands), 5)

	names := make(map[string]bool)
	for _, c := range resp.Categories[0].Commands {
		names[c.Name] = true
	}
	assert.True(t, names["/clear"])
	assert.True(t, names["/compact"])
	assert.True(t, names["/plan"])
	assert.True(t, names["/model"])
	assert.True(t, names["/memory"])
}

func TestSlashCommandService_ListCommands_Skills(t *testing.T) {
	svc, _, db, _ := setupSlashService(t)
	userID := "user-1"
	seedAgent(t, db, "skill-1", "搜索秘技", "必应搜索", model.AgentStatusApproved)
	seedAgent(t, db, "skill-2", "OCR", "图片转文字", model.AgentStatusApproved)
	seedAgent(t, db, "skill-3", "未激活", "未购买", model.AgentStatusApproved)
	seedPurchaseAndTool(t, db, userID, "skill-1")
	seedPurchaseAndTool(t, db, userID, "skill-2")

	resp, err := svc.ListCommands(userID)
	require.NoError(t, err)

	skills := resp.Categories[1]
	assert.Equal(t, SlashCategorySkills, skills.Name)
	require.Len(t, skills.Commands, 2)

	names := make(map[string]bool)
	for _, c := range skills.Commands {
		names[c.Name] = true
	}
	assert.True(t, names["/skill:skill-1"])
	assert.True(t, names["/skill:skill-2"])
}

func TestSlashCommandService_ListCommands_Templates(t *testing.T) {
	svc, _, _, tmpDir := setupSlashService(t)
	promptsDir := filepath.Join(tmpDir, "prompts")
	body := `---
description: 生成 PR 描述
argument_hint: "[改动摘要]"
---
请为以下改动写一段 PR 描述：
$ARGUMENTS`
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "pr-desc.md"), []byte(body), 0644))

	resp, err := svc.ListCommands("u1")
	require.NoError(t, err)

	templates := resp.Categories[2]
	assert.Equal(t, SlashCategoryTemplates, templates.Name)
	require.Len(t, templates.Commands, 1)
	assert.Equal(t, "/pr-desc", templates.Commands[0].Name)
	assert.Equal(t, "生成 PR 描述", templates.Commands[0].Description)
	assert.Equal(t, "[改动摘要]", templates.Commands[0].ArgumentsHint)
}

func TestSlashCommandService_FuzzyFiles(t *testing.T) {
	svc, _, _, tmpDir := setupSlashService(t)
	cwd := filepath.Join(tmpDir, "sandbox", "u1", "conversations", "conv1")
	require.NoError(t, os.MkdirAll(filepath.Join(cwd, "src"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "main.go"), []byte("package main"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "src", "util.go"), []byte("package src"), 0644))

	// Windows tmpDir 可能经 junction/symlink，ResolveProjectPath 内部会 EvalSymlinks，
	// 测试端也预先解析避免路径前缀不一致。
	cwd, err := filepath.EvalSymlinks(cwd)
	require.NoError(t, err)

	resp, err := svc.FuzzyFiles(cwd, "main", 10)
	require.NoError(t, err)
	require.Len(t, resp.Files, 1)
	assert.Equal(t, "main.go", resp.Files[0].Path)
	assert.Equal(t, "file", resp.Files[0].Type)
	assert.Greater(t, resp.Files[0].Score, float64(0))
}

func TestSlashCommandService_FuzzyFiles_TraversalRejected(t *testing.T) {
	svc, _, _, tmpDir := setupSlashService(t)
	cwd := filepath.Join(tmpDir, "sandbox", "u1", "conversations", "conv1")
	require.NoError(t, os.MkdirAll(cwd, 0755))

	// cwd 含 .. 应被 ResolveProjectPath 拒绝，返回空结果
	maliciousCwd := filepath.Join(cwd, "..", "..", "sandbox")
	resp, err := svc.FuzzyFiles(maliciousCwd, "secret", 10)
	require.NoError(t, err)
	assert.Len(t, resp.Files, 0)
}

func TestApplyPromptTemplate(t *testing.T) {
	cases := []struct {
		name string
		body string
		args []string
		want string
	}{
		{
			name: "arguments",
			body: "all: $ARGUMENTS, rest: $@, first: $1, second: $2",
			args: []string{"a", "b"},
			want: "all: a b, rest: a b, first: a, second: b",
		},
		{
			name: "missing arg",
			body: "first: $1, second: $2",
			args: []string{"only"},
			want: "first: only, second: ",
		},
		{
			name: "default value",
			body: "topic: ${1:-default topic}",
			args: []string{},
			want: "topic: default topic",
		},
		{
			name: "default overridden",
			body: "topic: ${1:-default topic}",
			args: []string{"custom"},
			want: "topic: custom",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplyPromptTemplate(tc.body, tc.args)
			assert.Equal(t, tc.want, got)
		})
	}
}
