// Package marketplace 内嵌官方模块目录。
//
// 安装版 claw（install 脚本分发的单文件二进制）启动时把官方模块播种到
// 本地 marketplace home（默认 ~/.eleball-claw/marketplace），用户在该目录
// 开发/管理模块，并用 `eleball-claw module up` 等命令经 docker compose 启动。
package marketplace

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

// FS 官方模块目录（stt / search-web / firecrawl / agent-reach / skill-maker / mcp-hello），含 module.json、
// docker-compose.yml、Dockerfile 与服务源码，足够在用户机器上 docker compose up。
// skill-maker 例外：它是 Prompt 型官方秘技，仅含 SKILL.md（SystemPrompt 文本），无容器。
//
//go:embed all:stt all:search-web all:firecrawl all:agent-reach all:skill-maker all:mcp-hello
var FS embed.FS

// SeedOfficial 把内嵌官方模块写入 root，只补缺失文件，绝不覆盖用户已有修改。
// 返回是否有文件被写入。
func SeedOfficial(root string) (bool, error) {
	seeded := false
	err := fs.WalkDir(FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(root, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// 已存在的文件跳过（用户可能改过 compose / module.json / 源码）
		if _, err := os.Stat(target); err == nil {
			return nil
		}
		data, err := FS.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
		seeded = true
		return nil
	})
	return seeded, err
}
