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
//   - 脚本模块（/studio 生成，marketplace/<id>/ 有文件）：递归打包模块目录内容。
//   - MCP 安装模块（InstallMCPRuntime 创建，DB-only 无文件）：从 SkillRuntime 物化 module.json 再打包。
//
// tar 条目相对模块根扁平存放（module.json / main.py / skus/*.json ...，无 <id>/ 前缀）。
// T11 云端审批通过后解压到 marketplace/<finalID>/，<finalID> 可由 cloud generateUniqueModuleID
// 据冲突重命名，故扁平布局使「解压到云端自定目录」最自然。module.json 内已含 source_origin/
// source_actor（脚本模块由 writeUserModuleJSON 写入、MCP 模块由 manifestFromSkillRuntime 物化），
// 云端扫描器（ensureMarketplaceModules）读回即还原 provenance，无需额外元数据文件。

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
// 要求目录内存在 module.json，否则产物无法被扫描器识别。
func packageModuleDir(moduleID, moduleDir string) (*PackageModuleResult, error) {
	if _, err := os.Stat(filepath.Join(moduleDir, "module.json")); err != nil {
		return nil, fmt.Errorf("模块目录 %s 缺少 module.json，无法打包: %w", moduleDir, err)
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

// packageMCPRuntime 从 SkillRuntime 物化 module.json 并打包（DB-only MCP 安装模块无磁盘文件）。
func packageMCPRuntime(moduleID string, rt *model.SkillRuntime) (*PackageModuleResult, error) {
	manifest := manifestFromSkillRuntime(rt)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化 module.json 失败: %w", err)
	}

	buf := &bytes.Buffer{}
	gw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{
		Name:     "module.json",
		Mode:     0o644,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		_ = tw.Close()
		_ = gw.Close()
		return nil, fmt.Errorf("写 module.json tar 头失败: %w", err)
	}
	if _, err := tw.Write(data); err != nil {
		_ = tw.Close()
		_ = gw.Close()
		return nil, fmt.Errorf("写 module.json 内容失败: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return &PackageModuleResult{Data: buf.Bytes(), Filename: moduleID + ".tar.gz"}, nil
}

// manifestFromSkillRuntime 把 SkillRuntime 反序列化为 marketplaceModuleManifest，
// 镜像 ensureMarketplaceModules 的读取侧，使物化的 module.json 可被扫描器原样读回。
// sku_scope：process 模块云端不做 autostart（见 firecrawl/README）-> claw；external(http) 可跨端 -> both。
func manifestFromSkillRuntime(rt *model.SkillRuntime) marketplaceModuleManifest {
	m := marketplaceModuleManifest{
		ID:              rt.ID,
		Name:            rt.Name,
		Description:     rt.Description,
		Source:          string(rt.Source),
		SourceOrigin:    string(rt.SourceOrigin),
		SourceActor:     rt.SourceActor,
		Transport:       string(rt.Transport),
		Deployment:      string(rt.Deployment),
		Endpoint:        rt.Endpoint,
		Command:         rt.Command,
		Args:            rt.ArgsList(),
		Env:             rt.EnvMap(),
		WorkDir:         rt.WorkDir,
		AutoSKU:         rt.AutoSKU,
		Credentials:     rt.CredentialsMap(),
		AllowedTools:    rt.AllowedToolsList(),
		DisallowedTools: rt.DisallowedToolsList(),
		Capabilities:    rt.CapabilitiesList(),
	}
	if cfg := rt.GetMCPServerConfig(); cfg != nil {
		m.MCPServerConfig = cfg
	}
	if rt.Deployment == model.SkillRuntimeDeploymentProcess {
		m.SKUScope = "claw"
	} else {
		m.SKUScope = "both"
	}
	m.Driver.ID = rt.DriverID
	m.Driver.Name = rt.Name
	m.Driver.Description = rt.Description
	return m
}
