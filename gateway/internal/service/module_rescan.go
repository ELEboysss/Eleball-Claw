package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
	"go.uber.org/zap"
)

// RescanPackage 统一重扫 marketplace/，物化 SkillRuntime + AgentItem（T2.2 单一实现）。
//
// 替换 RescanMarketplace / ensureMarketplaceModules / seed.SyncOfficialSKUs 的职责：
// 一次扫描同时补齐 SkillRuntime（tool/mcp 各一条）与 AgentItem（能力派生 + 手写 skus/*.json）。
//
// 每模块目录三种事实源（按优先级判定）：
//   - package.json（新格式，T5.1 官方模块迁移）：tool/mcpServers 各一条 SkillRuntime；
//     tools/skills/mcpServers 派生 AgentItem + 手写 skus/*.json 合并。
//   - module.json（legacy，T5.1 前兼容）：一条 SkillRuntime + 手写 skus/*.json。
//   - 仅 SKILL.md：prompt-only skill（Anthropic 标准，1 SKILL.md = 1 SKU，body 即 SystemPrompt）。
//
// side 决定 AgentItem 来源过滤（cloud 不收录 builtin，claw 收录全部）；
// SkillRuntime 不按 side 过滤（两侧 marketplace 目录物理分离，各自只含本端模块）。
// package.json 目录的 origin 优先读 .origin 侧车（T3.4 发布 user 包 / T4.2 claw 下载 cloud
// 包时写入；package.json 不声明 origin 防伪造），缺失按 side 默认（cloud→cloud，claw→builtin）。
//
// claw 分叉：根目录经包级 ResolveMarketplaceRoot() 解析（claw 无方法，见 resolver 差异）；
// 内嵌官方模块播种（SeedOfficial）由 seed 包包装器/启动入口负责，本函数只做纯扫描——
// 与 cloud 版语义等价，且便于测试（t.Setenv("CLAW_MARKETPLACE_DIR") 即可隔离根）。
//
// 幂等：重复扫描不重复创建；源消失（能力/文件删除）→ SKU 下架（delisted，保留购买记录）。
func (s *ModuleService) RescanPackage(side string, logger *zap.Logger) error {
	root := ResolveMarketplaceRoot()
	if root == "" {
		if logger != nil {
			logger.Warn("未找到 marketplace 目录，跳过模块物化")
		}
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if logger != nil {
			logger.Warn("读取 marketplace 目录失败，跳过模块物化", zap.Error(err))
		}
		return nil
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		modDir := filepath.Join(root, entry.Name())
		switch {
		case pathExists(filepath.Join(modDir, "package.json")):
			if err := s.materializePackageDir(modDir, side, logger); err != nil && logger != nil {
				logger.Warn("物化 package 目录失败", zap.String("dir", modDir), zap.Error(err))
			}
		case pathExists(filepath.Join(modDir, "module.json")):
			if err := s.materializeLegacyModuleDir(modDir, side, logger); err != nil && logger != nil {
				logger.Warn("物化 module 目录失败", zap.String("dir", modDir), zap.Error(err))
			}
		case pathExists(filepath.Join(modDir, "SKILL.md")):
			// prompt-only skill 目录（无 module.json/package.json，如 skill-maker/copywriting）
			s.syncPromptSkillSKU(root, entry.Name(), time.Now(), logger)
		}
	}
	return nil
}

// materializePackageDir 物化 package.json 目录（新格式）：
//   - SkillRuntime：tools 各一条（ID {pkg}-{tool}）+ mcpServers 各一条（ID {pkg}-mcp-{key}）。
//   - AgentItem：派生（tool→{pkg}-{tool} / skill→{pkg}-skill-{name} / mcp→{pkg}-mcp-{key}）
//     + 手写 skus/*.json 合并；源消失 → 下架。
func (s *ModuleService) materializePackageDir(modDir, side string, logger *zap.Logger) error {
	modName := filepath.Base(modDir)
	data, err := os.ReadFile(filepath.Join(modDir, "package.json"))
	if err != nil {
		return err
	}
	pkg, err := model.ParsePackageManifest(data)
	if err != nil {
		if logger != nil {
			logger.Warn("解析 package.json 失败", zap.String("dir", modDir), zap.Error(err))
		}
		return nil
	}

	origin := packageDirOrigin(modDir, side)
	official := origin.IsOfficial(modName)
	adminID := "00000000-0000-0000-0000-000000000000"
	now := time.Now()
	seen := map[string]bool{}

	// 1. tools → SkillRuntime + 派生 SKU
	for _, t := range pkg.Tools {
		rt := buildPackageToolRuntime(modName, pkg.Version, t, origin, official, modDir, side)
		s.upsertRuntime(rt, logger)
		skuID := modName + "-" + t.Name
		seen[skuID] = true
		s.upsertPackageToolSKU(modName, pkg, t, rt.ID, adminID, now, logger)
	}
	// 2. mcpServers → SkillRuntime + 派生 SKU
	//    auto_sku=true（D-A）：跳过通用 {pkg}-mcp-{key} SKU，由 DeriveSKUs 派生逐工具 SKU
	//    （mcp__{server}__{tool}，对齐 legacy firecrawl 无通用 SKU 的行为）。运行时仍注册——
	//    DeriveSKUs 在探活拿到 tools/list 后据此派生。旧通用 SKU 不在 seen 内 → delist 下架。
	for key, srv := range pkg.MCPServers {
		rt := buildPackageMCPRuntime(modName, pkg.Version, key, srv, origin, official, pkg.AutoSKU, modDir, side)
		s.upsertRuntime(rt, logger)
		skuID := modName + "-mcp-" + key
		if pkg.AutoSKU {
			continue // 不占 seen：旧通用 SKU（auto_sku 前的残留）交 delistStaleModuleSKUs 下架
		}
		seen[skuID] = true
		s.upsertPackageMCPDerivedSKU(modName, pkg, key, srv, rt.ID, adminID, now, logger)
	}
	// 3. skills → 派生 prompt-only SKU（body 取 skills/{name}/SKILL.md）
	for _, sk := range pkg.Skills {
		skuID := modName + "-skill-" + sk.Name
		seen[skuID] = true
		s.upsertPackageSkillSKU(modName, pkg, sk, modDir, adminID, now, logger)
	}
	// 4. 手写 skus/*.json 合并
	skuDir := filepath.Join(modDir, "skus")
	if entries, err := os.ReadDir(skuDir); err == nil {
		for _, sf := range entries {
			if sf.IsDir() || !strings.HasSuffix(sf.Name(), ".json") {
				continue
			}
			seen[modName+"-"+strings.TrimSuffix(sf.Name(), ".json")] = true
			s.upsertHandwrittenSKU(modName, skuDir, sf.Name(), adminID, now, logger)
		}
	}
	// 5. 下架源消失的 SKU（能力/文件删除；auto_sku 派生由 DeriveSKUs 管）
	s.delistStaleModuleSKUs(modName, seen, logger)
	return nil
}

// materializeLegacyModuleDir 物化 module.json 目录（legacy，T5.1 前兼容）。
// 逻辑 = 旧 ensureMarketplaceModules（SkillRuntime）+ seed.SyncOfficialSKUs 手写分支（AgentItem）。
func (s *ModuleService) materializeLegacyModuleDir(modDir, side string, logger *zap.Logger) error {
	modName := filepath.Base(modDir)
	path := filepath.Join(modDir, "module.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m marketplaceModuleManifest
	if err := json.Unmarshal(data, &m); err != nil {
		if logger != nil {
			logger.Warn("解析 module.json 失败", zap.String("path", path), zap.Error(err))
		}
		return nil
	}
	if m.ID == "" || m.Driver.ID == "" {
		if logger != nil {
			logger.Warn("module.json 缺少必填字段", zap.String("path", path))
		}
		return nil
	}

	// —— SkillRuntime upsert（原 ensureMarketplaceModules）——
	transport := parseSkillRuntimeTransport(m.Transport)
	deployment := parseSkillRuntimeDeployment(m.Deployment)
	endpoint := m.Endpoint
	if transport == model.SkillRuntimeTransportMCPHTTP && m.MCPServerConfig != nil {
		endpoint = m.MCPServerConfig.URL
	}

	origin := model.SkillRuntimeOrigin(m.Origin)
	official := origin.IsOfficial(m.ID)

	rt := &model.SkillRuntime{
		ID:                m.ID,
		Name:              m.Name,
		Description:       m.Description,
		Source:            model.SkillRuntimeSource(m.Source),
		Origin:            origin,
		Actor:             m.Actor,
		Transport:         transport,
		Deployment:        deployment,
		Endpoint:          endpoint,
		Command:           m.Command,
		WorkDir:           m.WorkDir,
		DockerComposePath: m.DockerComposePath,
		Official:          official,
		DriverID:          m.Driver.ID,
		AutoSKU:           m.AutoSKU,
		Version:           m.Version,
	}
	rt.SetArgs(m.Args)
	rt.SetEnv(m.Env)
	rt.SetCredentials(m.Credentials)
	rt.SetAllowedTools(m.AllowedTools)
	rt.SetDisallowedTools(m.DisallowedTools)
	if rt.Source == "" {
		rt.Source = model.SkillRuntimeSourceMarketplace
	}
	if rt.DockerComposePath == "" && deployment == model.SkillRuntimeDeploymentDocker {
		rt.DockerComposePath = filepath.Join(modDir, "docker-compose.yml")
	}
	// process 部署默认工作目录为模块目录（spawn 时 cmd.Dir），使相对 command/args 能定位模块脚本
	if deployment == model.SkillRuntimeDeploymentProcess && rt.WorkDir == "" {
		rt.WorkDir = modDir
	}
	rt.SetCapabilities(m.Capabilities)
	if transport == model.SkillRuntimeTransportMCPHTTP && m.MCPServerConfig != nil {
		rt.SetMCPServerConfig(m.MCPServerConfig)
	}

	// 状态保持（process 部署跨重启重置为 installed，disabled 保留）+ 注册，与 package 路径共用 upsertRuntime
	s.upsertRuntime(rt, logger)

	// —— AgentItem：手写 skus/*.json（原 seed.SyncOfficialSKUs module.json 分支）——
	if !originIncluded(m.Origin, side) {
		return nil
	}
	adminID := "00000000-0000-0000-0000-000000000000"
	now := time.Now()
	skuDir := filepath.Join(modDir, "skus")
	seen := map[string]bool{}
	if entries, err := os.ReadDir(skuDir); err == nil {
		for _, sf := range entries {
			if sf.IsDir() || !strings.HasSuffix(sf.Name(), ".json") {
				continue
			}
			seen[modName+"-"+strings.TrimSuffix(sf.Name(), ".json")] = true
			s.upsertHandwrittenSKU(modName, skuDir, sf.Name(), adminID, now, logger)
		}
	}
	// 下架源 skus/*.json 已删除的手写 SKU（auto 派生由 DeriveSKUs 管理，此处跳过）。
	// 前缀沿用目录名（与 package 路径/旧 seed.SyncOfficialSKUs 一致，m.ID==目录名）。
	s.delistStaleModuleSKUs(modName, seen, logger)
	return nil
}

// ===== SkillRuntime 构建 =====

// buildPackageToolRuntime 由 package.json tools[] 构建 SkillRuntime。
// tool 运行时 ID = DriverID = {pkg}-{tool}：SKU manifest.Driver 据此经 GetByDriverID 定位运行时。
// transport 映射：process→execute/process（WorkDir=模块目录）、docker→execute/docker（ImageRef）、
// http→raw_http/external（Endpoint）。
// side（D-C）：docker compose 文件按 side 选择（cloud=构建版 docker-compose.yml，claw=镜像版
// docker-compose.claw.yml），使同一 package.json 双端可物化。
func buildPackageToolRuntime(pkgName, version string, t model.PackageTool, origin model.SkillRuntimeOrigin, official bool, modDir, side string) *model.SkillRuntime {
	rt := &model.SkillRuntime{
		ID:          pkgName + "-" + t.Name,
		Name:        t.Name,
		Description: t.Description,
		Source:      model.SkillRuntimeSourceMarketplace,
		Origin:      origin,
		Official:    official,
		Version:     version,
		DriverID:    pkgName + "-" + t.Name,
		Status:      model.SkillRuntimeStatusInstalled,
	}
	switch t.Transport {
	case "process":
		rt.Transport = model.SkillRuntimeTransportExecute
		rt.Deployment = model.SkillRuntimeDeploymentProcess
		splitArgv(&rt.Command, rt, t.Command, t.Args)
		rt.WorkDir = modDir
	case "docker":
		rt.Transport = model.SkillRuntimeTransportExecute
		rt.Deployment = model.SkillRuntimeDeploymentDocker
		rt.ImageRef = t.Image
		rt.DockerComposePath = packageComposePath(modDir, side)
	case "http":
		rt.Transport = model.SkillRuntimeTransportRawHTTP
		rt.Deployment = model.SkillRuntimeDeploymentExternal
		rt.Endpoint = t.Endpoint
	}
	rt.SetEnv(t.Env)
	rt.SetCredentials(packageCredentialsToModel(t.Credentials))
	return rt
}

// buildPackageMCPRuntime 由 package.json mcpServers[] 构建 SkillRuntime（mcp 型）。
// stdio→mcp_stdio/process（Command+Args）；http/sse→mcp_http（Endpoint+MCPServerConfig，
// sse 优先后置 T2.5，此处按 http 连接）。
// autoSKU（D-A）：PackageManifest.auto_sku → rt.AutoSKU，探活成功后 DeriveSKUs 派生逐工具 SKU。
// side（D-C）决定 http/sse 的部署语义：
//   - claw：宿主进程需本地 docker 拉起容器并经发布端口访问 → Deployment=docker，
//     Endpoint=hostUrl（缺省回退 url），DockerComposePath=packageComposePath（.claw.yml 优先）。
//   - cloud：模块是预部署容器（deployments/docker-compose.prod.yml 随网关同网），只探活不 spawn →
//     Deployment=external，Endpoint=url。
// credentials（D-B）：mcpServers 凭证声明经 rt.SetCredentials 透传，逐工具派生 SKU 继承。
func buildPackageMCPRuntime(pkgName, version, key string, srv model.PackageMCPServer, origin model.SkillRuntimeOrigin, official, autoSKU bool, modDir, side string) *model.SkillRuntime {
	id := pkgName + "-mcp-" + key
	rt := &model.SkillRuntime{
		ID:          id,
		Name:        key,
		Description: "MCP 服务器 " + key,
		Source:      model.SkillRuntimeSourceMarketplace,
		Origin:      origin,
		Official:    official,
		Version:     version,
		DriverID:    id,
		AutoSKU:     autoSKU,
		Status:      model.SkillRuntimeStatusInstalled,
	}
	switch srv.Transport {
	case "stdio":
		rt.Transport = model.SkillRuntimeTransportMCPStdio
		rt.Deployment = model.SkillRuntimeDeploymentProcess
		splitArgv(&rt.Command, rt, srv.Command, srv.Args)
		rt.WorkDir = modDir // T3.1：stdio 子进程以模块目录为工作目录，相对 command/args 才能定位脚本（与 legacy process 一致）
		rt.SetMCPServerConfig(&model.MCPServerConfig{Command: rt.Command, Args: rt.ArgsList(), Env: srv.Env})
	case "http", "sse":
		rt.Transport = model.SkillRuntimeTransportMCPHTTP
		if side == "claw" {
			rt.Deployment = model.SkillRuntimeDeploymentDocker
			rt.DockerComposePath = packageComposePath(modDir, side)
			ep := srv.HostURL
			if ep == "" {
				ep = srv.URL
			}
			rt.Endpoint = ep
			rt.SetMCPServerConfig(&model.MCPServerConfig{URL: ep, Headers: srv.Headers})
		} else {
			rt.Deployment = model.SkillRuntimeDeploymentExternal
			rt.Endpoint = srv.URL
			rt.SetMCPServerConfig(&model.MCPServerConfig{URL: srv.URL, Headers: srv.Headers})
		}
	}
	rt.SetEnv(srv.Env)
	rt.SetCredentials(packageCredentialsToModel(srv.Credentials))
	return rt
}

// packageComposePath 按 side 选择 compose 文件：claw 优先 docker-compose.claw.yml（ACR 镜像 + 发布端口，
// claw 宿主机可拉取/访问），缺失或 cloud 用 docker-compose.yml（build 版，随云端部署构建）。
func packageComposePath(modDir, side string) string {
	if side == "claw" {
		clawPath := filepath.Join(modDir, "docker-compose.claw.yml")
		if pathExists(clawPath) {
			return clawPath
		}
	}
	return filepath.Join(modDir, "docker-compose.yml")
}

// splitArgv 把 package.json 的 argv（command[0]=可执行，其余=参数）拆进 SkillRuntime Command+Args。
func splitArgv(command *string, rt *model.SkillRuntime, argv, extra []string) {
	if len(argv) == 0 {
		return
	}
	*command = argv[0]
	args := append([]string{}, argv[1:]...)
	args = append(args, extra...)
	rt.SetArgs(args)
}

// upsertRuntime 注册 SkillRuntime（幂等）：新则 installed；已存在保留状态（process 部署跨重启
// 重置为 installed 由探活重新判定，disabled 保留）。
func (s *ModuleService) upsertRuntime(rt *model.SkillRuntime, logger *zap.Logger) {
	existing, err := s.repo.GetByID(rt.ID)
	if err != nil {
		existing = nil
	}
	if existing != nil {
		rt.CreatedAt = existing.CreatedAt
		rt.UpdatedAt = time.Now()
		if rt.Deployment == model.SkillRuntimeDeploymentProcess && existing.Status != model.SkillRuntimeStatusDisabled {
			rt.Status = model.SkillRuntimeStatusInstalled
		} else {
			rt.Status = existing.Status
		}
	} else {
		rt.Status = model.SkillRuntimeStatusInstalled
		rt.CreatedAt = time.Now()
		rt.UpdatedAt = rt.CreatedAt
	}
	if err := s.registry.Register(rt); err != nil && logger != nil {
		logger.Warn("注册 SkillRuntime 失败", zap.String("id", rt.ID), zap.Error(err))
	}
}

// ===== AgentItem 派生 =====

// upsertPackageToolSKU 派生 tool SKU：{pkg}-{tool}，driver=运行时 DriverID。
func (s *ModuleService) upsertPackageToolSKU(modName string, pkg *model.PackageManifest, t model.PackageTool, driverID, adminID string, now time.Time, logger *zap.Logger) {
	skuID := modName + "-" + t.Name
	params := packageParametersMap(t.Parameters)
	runtimeType := "remote"
	if t.Transport == "process" {
		runtimeType = "sidecar"
	}
	mf := model.ToolManifest{
		ID:             skuID,
		Name:           t.Name,
		Description:    t.Description,
		Driver:         model.ToolDriverType(driverID),
		Version:        pkg.Version, // T2.3：版本随派生源记录，T4.4 更新检测比对
		RuntimeType:    runtimeType,
		Category:       pkg.Category,
		Level:          pkg.Level,
		PriceDanwan:    packagePricingDanwan(t.Pricing),
		Parameters:     params,
		Actions:        []model.ToolAction{{Name: t.Name, Description: t.Description}},
		Metadata:       map[string]string{"module": modName, "package_module": modName, "package_derived": "tool"},
		Credentials:    packageCredentialsToModel(t.Credentials),
		TimeoutSeconds: t.TimeoutSeconds,
	}
	s.upsertSKUFromManifest(mf, adminID, now, logger)
}

// upsertPackageMCPDerivedSKU 派生 mcp SKU：{pkg}-mcp-{key}，driver=运行时 DriverID。
// Credentials（D-B）：mcpServers 凭证声明透传进 SKU manifest（auto_sku=false 的 MCP SKU 也带凭证，
// web 据此提示用户填写；auto_sku=true 时逐工具 SKU 经 buildDerivedManifest 的 rt.CredentialsMap 继承）。
func (s *ModuleService) upsertPackageMCPDerivedSKU(modName string, pkg *model.PackageManifest, key string, srv model.PackageMCPServer, driverID, adminID string, now time.Time, logger *zap.Logger) {
	skuID := modName + "-mcp-" + key
	mf := model.ToolManifest{
		ID:          skuID,
		Name:        key,
		Description: "MCP 服务器 " + key,
		Driver:      model.ToolDriverType(driverID),
		Version:     pkg.Version, // T2.3：版本随派生源记录
		RuntimeType: "remote",
		Category:    pkg.Category,
		Level:       pkg.Level,
		Parameters:  packageParametersMap(nil),
		Metadata:    map[string]string{"module": modName, "package_module": modName, "package_derived": "mcp"},
		Credentials: packageCredentialsToModel(srv.Credentials),
	}
	s.upsertSKUFromManifest(mf, adminID, now, logger)
}

// upsertPackageSkillSKU 派生 prompt-only skill SKU：{pkg}-skill-{name}，body 取 skills/{name}/SKILL.md
// 解析后的 Markdown body（SystemPrompt）。SKILL.md 缺失或 frontmatter 不合法则跳过（skill 无内容不可售）。
func (s *ModuleService) upsertPackageSkillSKU(modName string, pkg *model.PackageManifest, sk model.PackageSkill, modDir, adminID string, now time.Time, logger *zap.Logger) {
	skillmd, err := ParseSkillMD(filepath.Join(modDir, "skills", sk.Name, "SKILL.md"))
	if err != nil {
		if logger != nil {
			logger.Warn("解析 skill SKILL.md 失败，跳过派生", zap.String("dir", modDir), zap.String("skill", sk.Name), zap.Error(err))
		}
		return
	}
	skuID := modName + "-skill-" + sk.Name
	mf := model.ToolManifest{
		ID:          skuID,
		Name:        sk.Name,
		Description: sk.Description,
		Driver:      model.ToolDriverNone,
		Version:     pkg.Version, // T2.3：版本随派生源记录
		RuntimeType: "prompt",
		Category:    pkg.Category,
		Level:       pkg.Level,
		Parameters:  packageParametersMap(nil),
		Metadata:    map[string]string{"module": modName, "package_module": modName, "package_derived": "skill"},
	}
	s.upsertPromptSKU(mf, skillmd.Body, adminID, now, logger)
}

// upsertHandwrittenSKU 合并手写 skus/*.json（T2.3 契约）：按 ToolManifest upsert，per-field pin 保护。
func (s *ModuleService) upsertHandwrittenSKU(modName, skuDir, fileName, adminID string, now time.Time, logger *zap.Logger) {
	if s.agentRepo == nil {
		return // agentRepo 可选（struct 注释）；未注入时仅物化 SkillRuntime，跳过 AgentItem
	}
	skuID := modName + "-" + strings.TrimSuffix(fileName, ".json")
	fileStr, err := os.ReadFile(filepath.Join(skuDir, fileName))
	if err != nil {
		if logger != nil {
			logger.Warn("读 SKU manifest 失败", zap.String("path", filepath.Join(skuDir, fileName)), zap.Error(err))
		}
		return
	}
	var mf model.ToolManifest
	if err := json.Unmarshal(fileStr, &mf); err != nil {
		if logger != nil {
			logger.Warn("解析 SKU manifest 失败", zap.String("path", filepath.Join(skuDir, fileName)), zap.Error(err))
		}
		return
	}
	if mf.ID == "" || mf.Name == "" || mf.Driver == "" {
		if logger != nil {
			logger.Warn("SKU manifest 缺少必填字段，跳过", zap.String("path", filepath.Join(skuDir, fileName)))
		}
		return
	}
	mfStr := string(fileStr)
	existing, err := s.agentRepo.GetByID(skuID)
	if err != nil {
		existing = nil
	}
	if existing != nil {
		// 仅当文件加载成功（非空、非 "{}"）且与数据库内容不一致时才覆盖：
		// 既能让历史预置的旧 manifest 自动补齐后续新增字段，又避免容器内缺少 marketplace 目录时用空 JSON 冲掉有效数据。
		if !shouldSyncManifest(existing.ManifestJSON, mfStr) {
			return
		}
		if existing.SyncDerivedDisplay(mfStr, &mf) {
			if err := s.agentRepo.Update(existing); err != nil && logger != nil {
				logger.Warn("同步官方 SKU manifest 失败", zap.String("id", skuID), zap.Error(err))
			}
		}
		return
	}
	item := &model.AgentItem{
		ID:           skuID,
		Name:         mf.Name,
		Description:  mf.Description,
		Category:     mf.Category,
		Level:        model.AgentLevel(mf.Level),
		PriceDanwan:  mf.PriceDanwan,
		PriceElegant: mf.PriceElegant,
		Version:      mf.Version, // T2.3：手写 skus/*.json 可显式声明版本
		ManifestJSON: mfStr,
		Status:       model.AgentStatusApproved,
		CreatorID:    adminID,
		CreatorName:  "官方",
		CreatedAt:    now,
	}
	if err := s.agentRepo.Create(item); err != nil && logger != nil {
		logger.Warn("创建官方 SKU 失败", zap.String("id", skuID), zap.Error(err))
	}
}

// syncPromptSkillSKU 处理「只有 SKILL.md 无 module.json/package.json」的 prompt-only skill 目录。
// Anthropic 标准 SKILL.md 直接丢进 marketplace/ 即用：1 SKILL.md = 1 SKU（skillmd-<name>），
// body 即 SystemPrompt，不建 SkillRuntime（纯 prompt 无进程/docker）。driver=none。
func (s *ModuleService) syncPromptSkillSKU(root, modName string, now time.Time, logger *zap.Logger) {
	skillmd, err := ParseSkillMD(filepath.Join(root, modName, "SKILL.md"))
	if err != nil {
		// 无 SKILL.md 或 frontmatter 不合法：非 prompt-only skill 目录，静默跳过。
		return
	}
	adminID := "00000000-0000-0000-0000-000000000000"
	skuID := "skillmd-" + strings.TrimSpace(skillmd.Name)
	category := "提示"
	if skillmd.Metadata != nil {
		if c, ok := skillmd.Metadata["category"].(string); ok && strings.TrimSpace(c) != "" {
			category = c
		}
	}
	mf := model.ToolManifest{
		ID:          skuID,
		Name:        skillmd.Name,
		Description: skillmd.Description,
		Driver:      model.ToolDriverNone,
		Category:    category,
		Parameters:  map[string]interface{}{},
		Metadata:    map[string]string{"skillmd": "1"},
	}
	s.upsertPromptSKU(mf, skillmd.Body, adminID, now, logger)
}

// upsertPromptSKU 创建/同步 prompt-only SKU（SystemPrompt=body，driver=none）。
// SKILL.md 是源格式：body/name/desc 任一变化即同步（不同于 shouldSyncManifest 比 manifest_json）。
func (s *ModuleService) upsertPromptSKU(mf model.ToolManifest, body, adminID string, now time.Time, logger *zap.Logger) {
	if s.agentRepo == nil {
		return // agentRepo 可选；未注入时仅物化 SkillRuntime
	}
	skuID := mf.ID
	mfJSON, err := json.Marshal(mf)
	if err != nil {
		if logger != nil {
			logger.Warn("序列化 prompt-only skill manifest 失败", zap.String("id", skuID), zap.Error(err))
		}
		return
	}
	mfStr := string(mfJSON)
	existing, err := s.agentRepo.GetByID(skuID)
	if err != nil {
		existing = nil
	}
	if existing != nil {
		if existing.SystemPrompt == body && existing.Name == mf.Name && existing.Description == mf.Description && existing.Version == mf.Version {
			return
		}
		existing.ManifestJSON = mfStr
		existing.Name = mf.Name
		existing.Description = mf.Description
		existing.Category = mf.Category
		existing.Version = mf.Version // T2.3：版本变化（package 升级）触发刷新
		existing.SystemPrompt = body
		if existing.Status != model.AgentStatusApproved {
			existing.Status = model.AgentStatusApproved
		}
		if err := s.agentRepo.Update(existing); err != nil && logger != nil {
			logger.Warn("同步 prompt-only skill 失败", zap.String("id", skuID), zap.Error(err))
		}
		return
	}
	item := &model.AgentItem{
		ID:           skuID,
		Name:         mf.Name,
		Description:  mf.Description,
		Category:     mf.Category,
		Version:      mf.Version, // T2.3：skill 随 package.json version 记录
		SystemPrompt: body,
		ManifestJSON: mfStr,
		Status:       model.AgentStatusApproved,
		CreatorID:    adminID,
		CreatorName:  "官方",
		CreatedAt:    now,
	}
	if err := s.agentRepo.Create(item); err != nil && logger != nil {
		logger.Warn("创建 prompt-only skill SKU 失败", zap.String("id", skuID), zap.Error(err))
	}
}

// upsertSKUFromManifest 通用派生 SKU upsert：不存在则创建；存在则 per-field pin 感知同步展示字段。
func (s *ModuleService) upsertSKUFromManifest(mf model.ToolManifest, adminID string, now time.Time, logger *zap.Logger) {
	if s.agentRepo == nil {
		return // agentRepo 可选；未注入时仅物化 SkillRuntime
	}
	skuID := mf.ID
	mfJSON, err := json.Marshal(mf)
	if err != nil {
		if logger != nil {
			logger.Warn("序列化 SKU manifest 失败", zap.String("id", skuID), zap.Error(err))
		}
		return
	}
	mfStr := string(mfJSON)
	existing, err := s.agentRepo.GetByID(skuID)
	if err != nil {
		existing = nil
	}
	if existing != nil {
		// per-field pin：admin 钉住的展示字段不被派生覆写；manifest_json(派生源) 与 Category 始终同步。
		if existing.SyncDerivedDisplay(mfStr, &mf) {
			if existing.Status != model.AgentStatusApproved {
				existing.Status = model.AgentStatusApproved
			}
			if err := s.agentRepo.Update(existing); err != nil && logger != nil {
				logger.Warn("更新 SKU 失败", zap.String("id", skuID), zap.Error(err))
			}
		}
		return
	}
	item := &model.AgentItem{
		ID:           skuID,
		Name:         mf.Name,
		Description:  mf.Description,
		Category:     mf.Category,
		Level:        model.AgentLevel(mf.Level),
		PriceDanwan:  mf.PriceDanwan,
		PriceElegant: mf.PriceElegant,
		Version:      mf.Version, // T2.3：版本随派生源记录
		ManifestJSON: mfStr,
		Status:       model.AgentStatusApproved,
		CreatorID:    adminID,
		CreatorName:  "官方",
		CreatedAt:    now,
	}
	if err := s.agentRepo.Create(item); err != nil && logger != nil {
		logger.Warn("创建 SKU 失败", zap.String("id", skuID), zap.Error(err))
	}
}

// delistStaleModuleSKUs 下架模块源已消失的 SKU（能力删除 / skus/*.json 删除），保留购买记录不硬删。
// auto_sku 派生的 SKU（manifest 标记 auto_sku_module）由 DeriveSKUs 精确管理，此处跳过。
// 安全前提：模块名之间不存在「A 是 B 的前缀 + '-'」关系（marketplace 模块名已校验无碰撞），
// 故 ListByModuleSKUs 的 id LIKE '<mod>-%' 粗筛不会命中间名前缀的其他模块 SKU。
func (s *ModuleService) delistStaleModuleSKUs(modName string, seenIDs map[string]bool, logger *zap.Logger) {
	if s.agentRepo == nil {
		return // agentRepo 可选；未注入时跳过 SKU 下架
	}
	existing, err := s.agentRepo.ListByModuleSKUs(modName)
	if err != nil {
		if logger != nil {
			logger.Warn("查询模块现有 SKU 失败，跳过下架", zap.String("module", modName), zap.Error(err))
		}
		return
	}
	for _, it := range existing {
		if it.Status != model.AgentStatusApproved {
			continue
		}
		if seenIDs[it.ID] {
			continue
		}
		mf, err := it.Manifest()
		if err != nil || mf == nil {
			continue
		}
		if mf.Metadata["auto_sku_module"] != "" {
			continue // auto 派生 SKU，由 DeriveSKUs 管理
		}
		if err := s.agentRepo.UpdateStatus(it.ID, model.AgentStatusDelisted); err != nil {
			if logger != nil {
				logger.Warn("下架陈旧 SKU 失败", zap.String("id", it.ID), zap.Error(err))
			}
			continue
		}
		if logger != nil {
			logger.Info("下架陈旧 SKU（源文件/能力已删除）", zap.String("id", it.ID), zap.String("module", modName))
		}
	}
}

// ===== 辅助 =====

// pathExists 判断路径为存在的文件。
func pathExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// packageDirOrigin 解析 package.json 目录的 origin（package.json 不声明 origin 防伪造）：
// 优先读 .origin 侧车（T3.4/T4.2 写入），缺失按 side 默认。
func packageDirOrigin(modDir, side string) model.SkillRuntimeOrigin {
	if b, err := os.ReadFile(filepath.Join(modDir, ".origin")); err == nil {
		switch strings.TrimSpace(string(b)) {
		case string(model.SkillRuntimeOriginBuiltin):
			return model.SkillRuntimeOriginBuiltin
		case string(model.SkillRuntimeOriginCloud):
			return model.SkillRuntimeOriginCloud
		case string(model.SkillRuntimeOriginUser):
			return model.SkillRuntimeOriginUser
		}
	}
	if side == "claw" {
		return model.SkillRuntimeOriginBuiltin
	}
	return model.SkillRuntimeOriginCloud
}

// packageParametersMap 把 PackageToolParameters（OpenAI schema）转成 ToolManifest.Parameters map。
func packageParametersMap(p *model.PackageToolParameters) map[string]interface{} {
	if p == nil {
		return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
	}
	m := map[string]interface{}{"type": p.Type, "properties": p.Properties}
	if len(p.Required) > 0 {
		m["required"] = p.Required
	}
	return m
}

// packageCredentialsToModel 转换凭证声明：PackageCredential → CredentialDef。
// Scope 固定 module：package 凭证声明挂在 mcpServer/tool 上，同 server 派生的所有 SKU 共享一份凭证值
// （存 module:<driver> 桶，与 legacy module.json 显式 "scope":"module" 对齐）；T5.1 agent-reach 21 个
// 派生 SKU 共用同一 cookie 桶即由此而来。空 Scope 默认 sku 会要求每个 SKU 各填一份，不可取。
func packageCredentialsToModel(creds map[string]model.PackageCredential) map[string]model.CredentialDef {
	if len(creds) == 0 {
		return nil
	}
	out := make(map[string]model.CredentialDef, len(creds))
	for k, c := range creds {
		out[k] = model.CredentialDef{
			Type:        model.CredentialType(c.Type),
			Label:       c.Label,
			Description: c.Description,
			Placeholder: c.Placeholder,
			Required:    c.Required,
			Scope:       model.CredentialScopeModule,
		}
	}
	return out
}

// packagePricingDanwan 提取 per_call/danwan 定价为 AgentItem.PriceDanwan（免费返回 0）。
func packagePricingDanwan(p *model.PackagePricing) int64 {
	if p == nil || p.Type != "per_call" || p.Currency != "danwan" {
		return 0
	}
	return int64(p.AmountPerCall)
}

// originIncluded 判断该模块的 origin 是否被当前 side 收录（T1.3 取代旧 scopeIncluded）。
// cloud 侧不收录 builtin（claw 内置模块，如 mcp-stdio-echo 不应出现在云端 seed/catalog）；
// claw 侧收录本地 marketplace 全部（builtin 内置 + cloud 官方副本 + user 本地创作）。
func originIncluded(origin, side string) bool {
	if side == "claw" {
		return true
	}
	return origin != "builtin"
}

// shouldSyncManifest 判断是否需要用 marketplace 文件中的 manifest 覆盖数据库值。
// 仅当文件加载成功（非空、非 "{}"）且与数据库内容不一致时才覆盖。
func shouldSyncManifest(existing, fromFile string) bool {
	if fromFile == "" || fromFile == "{}" {
		return false
	}
	return strings.TrimSpace(existing) != strings.TrimSpace(fromFile)
}
