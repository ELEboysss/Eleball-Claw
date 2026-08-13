package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/eleball/gateway/internal/model"
)

// 本文件实现 T7：本地模块产物打包，供 T8「分享到云端」上传审核。
//
// 两类模块：
//   - 秘技包模块（/studio 生成，marketplace/<id>/ 有文件）：递归打包模块目录内容
//     （package.json + main.py + skills/ + skus/ + .origin，T3.2 对齐 package 布局）。
//   - MCP 安装模块（InstallMCPRuntime 创建，DB-only 无文件）：从 SkillRuntime 物化
//     package.json（mcpServers 描述）再打包。
//
// tar 条目相对模块根扁平存放（package.json / main.py / skus/*.json ...，无 <id>/ 前缀）。
// T11 云端审批通过后解压到 marketplace/<finalID>/，<finalID> 可由 cloud generateUniqueModuleID
// 据冲突重命名，故扁平布局使「解压到云端自定目录」最自然。package.json 不声明 origin 防伪造
// （T1.3），provenance 走 .origin 侧车随包传输：脚本模块由 writeUserPackageJSON 写入 user，
// MCP 模块由 packageMCPRuntime 写入 rt.Origin，云端读回即还原 origin，无需额外元数据文件。

// moduleArchiveExcludes 打包时排除的构建产物/系统文件，避免把 __pycache__/*.pyc 等带入分享产物。
var moduleArchiveExcludes = map[string]bool{
	"__pycache__": true,
	".DS_Store":   true,
}

// PackageModuleResult 模块打包结果。
type PackageModuleResult struct {
	Data     []byte // tar.gz 字节流
	Filename string // 建议文件名 <moduleID>.tar.gz
}

// PackageModule 把本地模块打成 tar.gz 产物，供 T8 上传云端审核。
//   - 脚本模块（marketplace/<id>/ 有目录）：递归打包模块目录内容（扁平条目），排除构建产物。
//   - MCP 安装模块（DB-only，无目录）：从 SkillRuntime 物化 module.json 再打包。
//
// 优先取磁盘目录（脚本模块的 main.py/skus/ 只在磁盘上，是忠实来源）；无目录才回落到 DB 物化。
func (s *ModuleService) PackageModule(moduleID string) (*PackageModuleResult, error) {
	if moduleID == "" {
		return nil, errors.New("module_id 不能为空")
	}

	root := ResolveMarketplaceRoot()
	if root != "" {
		moduleDir := filepath.Join(root, moduleID)
		if fi, err := os.Stat(moduleDir); err == nil && fi.IsDir() {
			return packageModuleDir(moduleID, moduleDir)
		}
	}

	// 无磁盘目录：按 DB-only MCP 安装模块处理，从 SkillRuntime 物化 module.json。
	rt, err := s.repo.GetByID(moduleID)
	if err != nil || rt == nil {
		return nil, fmt.Errorf("模块 %s 无本地目录且未在运行时注册，无法打包", moduleID)
	}
	return packageMCPRuntime(moduleID, rt)
}

// packageModuleDir 递归打包脚本模块目录（扁平条目，排除构建产物）。
// T3.2：要求目录内存在 package.json（秘技包布局）或遗留 module.json，否则产物无法被扫描器识别。
func packageModuleDir(moduleID, moduleDir string) (*PackageModuleResult, error) {
	if _, perr := os.Stat(filepath.Join(moduleDir, "package.json")); perr != nil {
		if _, merr := os.Stat(filepath.Join(moduleDir, "module.json")); merr != nil {
			return nil, fmt.Errorf("模块目录 %s 缺少 package.json（或遗留 module.json），无法打包: %w", moduleDir, merr)
		}
	}

	buf := &bytes.Buffer{}
	gw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gw)

	err := filepath.WalkDir(moduleDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(moduleDir, path)
		rel = filepath.ToSlash(rel)
		name := d.Name()

		// 排除构建产物/系统文件：目录整棵跳过，文件跳过单条。
		if moduleArchiveExcludes[name] || strings.HasSuffix(name, ".pyc") || strings.HasSuffix(name, ".pyo") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "." {
			return nil // tar 不需要模块根自身的目录条目
		}
		if d.IsDir() {
			return nil // 目录由其下文件条目按需创建，无需单独写 dir header
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil // 跳过符号链接等非常规文件，分享产物只含常规文件
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name:     rel,
			Mode:     int64(info.Mode().Perm()),
			Size:     int64(len(data)),
			ModTime:  info.ModTime(),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		_ = tw.Close()
		_ = gw.Close()
		return nil, fmt.Errorf("打包模块目录失败: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("关闭 tar 写入失败: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("关闭 gzip 失败: %w", err)
	}
	return &PackageModuleResult{Data: buf.Bytes(), Filename: moduleID + ".tar.gz"}, nil
}

// packageMCPRuntime 从 SkillRuntime 物化 package.json + .origin 并打包（DB-only MCP 安装模块无磁盘文件）。
func packageMCPRuntime(moduleID string, rt *model.SkillRuntime) (*PackageModuleResult, error) {
	manifest := manifestFromSkillRuntime(rt)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化 package.json 失败: %w", err)
	}

	buf := &bytes.Buffer{}
	gw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gw)
	writeEntry := func(name string, content []byte, mode int64) error {
		hdr := &tar.Header{Name: name, Mode: mode, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("写 %s tar 头失败: %w", name, err)
		}
		if _, err := tw.Write(content); err != nil {
			return fmt.Errorf("写 %s 内容失败: %w", name, err)
		}
		return nil
	}
	if err := writeEntry("package.json", data, 0o644); err != nil {
		_ = tw.Close()
		_ = gw.Close()
		return nil, err
	}
	// .origin 侧车随包传输（package.json 不声明 origin 防伪造，T1.3）：云端读回还原 provenance。
	if err := writeEntry(".origin", []byte(string(rt.Origin)), 0o644); err != nil {
		_ = tw.Close()
		_ = gw.Close()
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return &PackageModuleResult{Data: buf.Bytes(), Filename: moduleID + ".tar.gz"}, nil
}

// manifestFromSkillRuntime 把 SkillRuntime 反序列化为 model.PackageManifest（T3.2 改写 package.json）。
// mcpServers.main 描述该 MCP 服务器：stdio=command/args/env，http/sse=url/headers。
// version 缺省 0.1.0；author 取 actor（MCP 安装模块 fold 进 user 时 actor=MCP 名）。
// 用 GetMCPServerConfig（权威配置）优先，缺省回退 rt 顶层字段。
func manifestFromSkillRuntime(rt *model.SkillRuntime) model.PackageManifest {
	version := rt.Version
	if version == "" {
		version = "0.1.0"
	}
	m := model.PackageManifest{
		Name:        rt.ID,
		Version:     version,
		Description: rt.Description,
		Author:      rt.Actor,
		Level:       1,
	}

	srv := model.PackageMCPServer{}
	if cfg := rt.GetMCPServerConfig(); cfg != nil {
		srv.Env = cfg.Env
		srv.Headers = cfg.Headers
		srv.URL = cfg.URL
		argv := append([]string{}, cfg.Command)
		argv = append(argv, cfg.Args...)
		srv.Command = argv
	} else {
		argv := append([]string{rt.Command}, rt.ArgsList()...)
		srv.Command = argv
		srv.Env = rt.EnvMap()
		srv.URL = rt.Endpoint
	}
	switch rt.Transport {
	case model.SkillRuntimeTransportMCPHTTP:
		srv.Transport = "http"
	case model.SkillRuntimeTransportMCPSSE:
		srv.Transport = "sse"
	default:
		srv.Transport = "stdio"
	}
	m.MCPServers = map[string]model.PackageMCPServer{"main": srv}
	return m
}
