package service

// interpreter_bootstrap.go 托管解释器引导（H1，降低安装成本最大单项）。
//
// 对标 providers/openhuman/runtime_python/{bootstrap.rs,downloader.rs}，翻译为 Go。
// 用户无系统 python 时，自动下载 astral-sh/python-build-standalone 的 install_only
// 发行版，经 SHA-256 校验后解压安装到 ~/.eleball-claw/tools/python；locateCommand
// 在系统未找到时回退到该托管二进制（仅廉价磁盘探测，不触发下载）。
//
// 设计约束：
//   - locateCommand（spawn/probe 路径）只做磁盘探测，绝不联网下载——避免 stdio
//     autostart/probe 阻塞数分钟。真正下载由显式动作触发：POST /claw-console/tools/
//     install-interpreter 端点 或 eleball-claw setup-python 子命令。
//   - 下载信任：python-build-standalone 来自 astral-sh 官方，资产 SHA-256 取自同发布
//     的 SHA256SUMS 文件（非依赖 GitHub API 可能缺失的 digest 字段），校验失败拒绝安装。
//     node 同理取自 nodejs.org 官方 <version>/SHASUMS256.txt，校验失败拒绝安装。
//   - 支持 python 家族与 node/npx（npx 随 node 发行版自带）。locateCommand 在系统未找到
//     时回退已安装的托管二进制；node/npx 经 managedNodeBinDir 把 node 放上 PATH 供 npx
//     脚本 shebang 解析（spawn 侧 ensureNodePath，见 skill_runtime_manager）。

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	// pythonBuildStandaloneReleasesAPI astral-sh/python-build-standalone GitHub releases API。
	pythonBuildStandaloneReleasesAPI = "https://api.github.com/repos/astral-sh/python-build-standalone/releases"
	// managedPythonMinVersion 托管 Python 最低版本（install_only 稳定可用的下限）。
	managedPythonMinVersion = "3.12.0"
	// managedPythonMaxVersion 托管 Python 版本上界（排他，避开 3.14 预发布系列）。
	managedPythonMaxVersion = "3.14.0"
	// sha256SumsAssetName 发布中携带的校验和文件资产名。
	sha256SumsAssetName = "SHA256SUMS"
	// downloadUserAgent GitHub API 与对象存储都要求 UA。
	downloadUserAgent = "eleball-claw/interpreter-bootstrap"
	// nodeDistIndexURL nodejs.org 官方发行版索引（JSON 数组，含 version/lts/security 等字段）。
	nodeDistIndexURL = "https://nodejs.org/dist/index.json"
	// nodeDistBaseURL nodejs.org 发行版下载基址（按版本拼接子路径）。
	nodeDistBaseURL = "https://nodejs.org/dist"
	// nodeShasumsName nodejs.org 每个版本目录下的校验和文件名（格式同 SHA256SUMS）。
	nodeShasumsName = "SHASUMS256.txt"
)

// githubRelease python-build-standalone 发布元数据（仅取需要的字段）。
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

// githubAsset 单个发布资产。digest 为 GitHub API 可选字段，可能为空——
// 实际校验值优先取 SHA256SUMS 文件，digest 仅作兜底。
type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

// pythonDistribution 选定的可下载 Python 发行版。
type pythonDistribution struct {
	releaseTag string
	assetName  string
	url        string
	version    pythonVersion
	digest     string // API digest 字段（可能空），仅 SHA256SUMS 缺失时兜底
}

// InterpreterBootstrap 托管解释器引导器。
// 持有 HTTP 客户端（下载用）与进程内互斥锁（串行化下载，防并发重复下载）。
type InterpreterBootstrap struct {
	logger *zap.Logger
	mu     sync.Mutex
	client *http.Client
}

// ResolvedInterpreter 解析后的解释器。
type ResolvedInterpreter struct {
	Command string // 原始命令名（python/python3/...）
	Path    string // 二进制绝对路径
	Version string // 版本字符串
	Source  string // "system" | "managed"
	Reused  bool   // 是否复用已存在的（未新下载）
}

// NewInterpreterBootstrap 创建引导器。client 为 nil 时用默认 5min 超时客户端。
func NewInterpreterBootstrap(logger *zap.Logger) *InterpreterBootstrap {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &InterpreterBootstrap{
		logger: logger,
		client: &http.Client{Timeout: 5 * time.Minute},
	}
}

// EnsureInterpreter 确保指定命令对应的解释器可用：系统优先，回退托管（自动下载安装）。
// command 支持 python/python3/python3.12 等（统一按 python 处理）与 node/npx（统一按 node
// 处理，npx 随 node 发行版自带）；其他命令仅系统查找，缺失返回错误。
func (b *InterpreterBootstrap) EnsureInterpreter(ctx context.Context, command string) (*ResolvedInterpreter, error) {
	if isNodeCommand(command) {
		return b.ensureNode(ctx, command)
	}
	if !isPythonCommand(command) {
		// 未知命令：仅尝试系统查找，缺失即报错（不下载）。
		if path, err := exec.LookPath(command); err == nil {
			return &ResolvedInterpreter{Command: command, Path: path, Source: "system", Reused: true}, nil
		}
		return nil, fmt.Errorf("暂不支持托管的解释器 %q，请手动安装", command)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// 1. 系统优先：python -> python3 依次探测。
	for _, cmd := range []string{"python", "python3"} {
		if path, err := exec.LookPath(cmd); err == nil {
			ver, _ := probeCommandVersion(path)
			return &ResolvedInterpreter{Command: command, Path: path, Version: ver, Source: "system", Reused: true}, nil
		}
	}

	// 2. 托管已安装（廉价磁盘探测，不联网）。
	if bin := findManagedPythonBin(managedPythonDir()); bin != "" {
		ver, _ := probeCommandVersion(bin)
		b.logger.Info("复用已安装的托管 Python", zap.String("path", bin), zap.String("version", ver))
		return &ResolvedInterpreter{Command: command, Path: bin, Version: ver, Source: "managed", Reused: true}, nil
	}

	// 3. 下载安装托管。
	b.logger.Info("系统未找到 Python，开始下载托管发行版",
		zap.String("min", managedPythonMinVersion), zap.String("max", managedPythonMaxVersion))
	installed, err := b.installManagedPython(ctx)
	if err != nil {
		return nil, err
	}
	installed.Command = command
	installed.Source = "managed"
	installed.Reused = false
	return installed, nil
}

// installManagedPython 下载、校验、解压、安装托管 Python。
func (b *InterpreterBootstrap) installManagedPython(ctx context.Context) (*ResolvedInterpreter, error) {
	release, err := b.fetchLatestRelease(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取 python-build-standalone 发布信息失败: %w", err)
	}
	dist, err := selectPythonDistribution(release.Assets)
	if err != nil {
		return nil, fmt.Errorf("选择发行版失败: %w", err)
	}

	expected, err := b.resolveExpectedSHA256(ctx, release.Assets, dist)
	if err != nil {
		return nil, err
	}

	toolsDir := managedToolsDir()
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建工具目录失败: %w", err)
	}
	archive := filepath.Join(toolsDir, dist.assetName)
	b.logger.Info("下载托管 Python",
		zap.String("asset", dist.assetName),
		zap.String("version", dist.version.String()),
		zap.String("release", release.TagName))
	if err := downloadAndVerify(ctx, b.client, dist.url, expected, archive); err != nil {
		return nil, fmt.Errorf("下载/校验失败: %w", err)
	}

	// 解压到 staging 目录，再原子移动到目标目录。
	staging := filepath.Join(toolsDir, fmt.Sprintf(".stage-python-%d", os.Getpid()))
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return nil, fmt.Errorf("创建暂存目录失败: %w", err)
	}
	f, err := os.Open(archive)
	if err != nil {
		_ = os.RemoveAll(staging)
		return nil, err
	}
	extractErr := extractTarGz(ctx, f, staging)
	_ = f.Close()
	if extractErr != nil {
		_ = os.RemoveAll(staging)
		return nil, fmt.Errorf("解压失败: %w", extractErr)
	}

	// install_only 资产顶层为 python/ 目录；找不到则扫描 staging 顶层目录。
	extracted := filepath.Join(staging, "python")
	if _, err := os.Stat(extracted); err != nil {
		extracted = findPythonRoot(staging)
		if extracted == "" {
			_ = os.RemoveAll(staging)
			return nil, errors.New("解压后未找到 python 目录")
		}
	}

	dest := managedPythonDir()
	_ = os.RemoveAll(dest)
	if err := os.Rename(extracted, dest); err != nil {
		_ = os.RemoveAll(staging)
		return nil, fmt.Errorf("安装移动失败: %w", err)
	}
	_ = os.RemoveAll(staging)
	_ = os.Remove(archive)

	bin := findManagedPythonBin(dest)
	if bin == "" {
		return nil, errors.New("安装完成但未找到 python 二进制")
	}
	ver, _ := probeCommandVersion(bin)
	b.logger.Info("托管 Python 安装完成", zap.String("path", bin), zap.String("version", ver))
	return &ResolvedInterpreter{Path: bin, Version: ver}, nil
}

// --- 托管 Node.js（nodejs.org 官方发行版）---

// ensureNode 确保 node/npx 可用：系统优先 -> 托管已装（磁盘探测）-> 下载安装。
// npx 随 node 发行版自带，故统一安装 node，再按 command 取对应二进制。
func (b *InterpreterBootstrap) ensureNode(ctx context.Context, command string) (*ResolvedInterpreter, error) {
	// 1. 系统优先。
	if path, err := exec.LookPath(command); err == nil {
		ver, _ := probeNodeVersion(path)
		return &ResolvedInterpreter{Command: command, Path: path, Version: ver, Source: "system", Reused: true}, nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// 2. 托管已安装（廉价磁盘探测，不联网）。
	if bin := findManagedNodeBin(managedNodeDir(), command); bin != "" {
		ver, _ := probeNodeVersion(bin)
		b.logger.Info("复用已安装的托管 Node", zap.String("path", bin), zap.String("version", ver))
		return &ResolvedInterpreter{Command: command, Path: bin, Version: ver, Source: "managed", Reused: true}, nil
	}

	// 3. 下载安装托管 node。
	b.logger.Info("系统未找到 Node.js，开始下载托管发行版")
	if err := b.installManagedNode(ctx); err != nil {
		return nil, err
	}
	bin := findManagedNodeBin(managedNodeDir(), command)
	if bin == "" {
		return nil, errors.New("托管 Node 安装完成但未找到 " + command + " 二进制")
	}
	ver, _ := probeNodeVersion(bin)
	b.logger.Info("托管 Node 安装完成", zap.String("path", bin), zap.String("version", ver))
	return &ResolvedInterpreter{Command: command, Path: bin, Version: ver, Source: "managed", Reused: false}, nil
}

// nodeDistEntry nodejs.org dist/index.json 单条记录（仅取需要的字段）。
type nodeDistEntry struct {
	Version string      `json:"version"` // 形如 "v20.15.0"
	LTS     interface{} `json:"lts"`     // false（非 LTS）或代号字符串（LTS）
}

// installManagedNode 下载、校验、解压、安装托管 Node.js。
func (b *InterpreterBootstrap) installManagedNode(ctx context.Context) error {
	version, err := b.fetchLatestLTSNodeVersion(ctx)
	if err != nil {
		return fmt.Errorf("获取 nodejs.org LTS 版本失败: %w", err)
	}
	assetName, err := nodeAssetName(version)
	if err != nil {
		return err
	}
	url := nodeDistBaseURL + "/" + version + "/" + assetName
	sumsURL := nodeDistBaseURL + "/" + version + "/" + nodeShasumsName
	expected, err := b.fetchNodeSHA256(ctx, sumsURL, assetName)
	if err != nil {
		return fmt.Errorf("获取 %s 校验值失败: %w", assetName, err)
	}

	toolsDir := managedToolsDir()
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		return fmt.Errorf("创建工具目录失败: %w", err)
	}
	archive := filepath.Join(toolsDir, assetName)
	b.logger.Info("下载托管 Node", zap.String("asset", assetName), zap.String("version", version))
	if err := downloadAndVerify(ctx, b.client, url, expected, archive); err != nil {
		return fmt.Errorf("下载/校验失败: %w", err)
	}

	// 解压到 staging，再原子移动到目标目录。
	staging := filepath.Join(toolsDir, fmt.Sprintf(".stage-node-%d", os.Getpid()))
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return fmt.Errorf("创建暂存目录失败: %w", err)
	}
	var extractErr error
	if strings.HasSuffix(assetName, ".zip") {
		extractErr = extractZip(archive, staging)
	} else {
		f, oErr := os.Open(archive)
		if oErr != nil {
			_ = os.RemoveAll(staging)
			return oErr
		}
		extractErr = extractTarGz(ctx, f, staging)
		_ = f.Close()
	}
	if extractErr != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("解压失败: %w", extractErr)
	}

	// node 资产顶层为 node-v{ver}-{plat}-{arch}/ 目录；找不到则扫描 staging 顶层。
	extracted := findNodeRoot(staging)
	if extracted == "" {
		_ = os.RemoveAll(staging)
		return errors.New("解压后未找到 node 目录")
	}
	dest := managedNodeDir()
	_ = os.RemoveAll(dest)
	if err := os.Rename(extracted, dest); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("安装移动失败: %w", err)
	}
	_ = os.RemoveAll(staging)
	_ = os.Remove(archive)
	return nil
}

// fetchLatestLTSNodeVersion 取 nodejs.org dist/index.json 中最新 LTS 版本（lts 非 false 的最高 semver）。
func (b *InterpreterBootstrap) fetchLatestLTSNodeVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nodeDistIndexURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", downloadUserAgent)
	resp, err := b.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("nodejs.org index.json 状态 %d", resp.StatusCode)
	}
	var entries []nodeDistEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return "", err
	}
	return selectNodeLTSEntry(entries)
}

// fetchNodeSHA256 取 nodejs.org <version>/SHASUMS256.txt 中 assetName 对应的校验值。
// nodejs.org 无 per-asset digest API，校验值仅来自此文件；缺失则拒绝安装（H1 下载信任）。
func (b *InterpreterBootstrap) fetchNodeSHA256(ctx context.Context, sumsURL, assetName string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", downloadUserAgent)
	resp, err := b.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SHASUMS256.txt 状态 %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	sums := parseSHA256Sums(string(body))
	if expected, ok := sums[assetName]; ok {
		return expected, nil
	}
	return "", fmt.Errorf("SHASUMS256.txt 未包含 %s", assetName)
}

// selectNodeLTSEntry 从 dist/index.json 记录中选 lts 非 false 的最高 semver，返回版本字符串。
func selectNodeLTSEntry(entries []nodeDistEntry) (string, error) {
	var best string
	var bestVer pythonVersion
	for _, e := range entries {
		if e.LTS == nil || e.LTS == false {
			continue
		}
		ver, ok := parseNodeVersion(e.Version)
		if !ok {
			continue
		}
		if best == "" || ver.gte(bestVer) {
			best = e.Version
			bestVer = ver
		}
	}
	if best == "" {
		return "", errors.New("nodejs.org 未找到 LTS 版本")
	}
	return best, nil
}

// nodeAssetName 据主机平台/架构拼装 node 资产名（含扩展名）。
func nodeAssetName(version string) (string, error) {
	plat, arch, ext, err := nodeHostTriple()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("node-%s-%s-%s%s", version, plat, arch, ext), nil
}

// nodeHostTriple 返回当前主机的 node 资产三元组（platform、arch、扩展名）。
func nodeHostTriple() (plat, arch, ext string, err error) {
	switch {
	case runtime.GOOS == "windows" && runtime.GOARCH == "amd64":
		return "win", "x64", ".zip", nil
	case runtime.GOOS == "windows" && runtime.GOARCH == "arm64":
		return "win", "arm64", ".zip", nil
	case runtime.GOOS == "linux" && runtime.GOARCH == "amd64":
		return "linux", "x64", ".tar.gz", nil
	case runtime.GOOS == "linux" && runtime.GOARCH == "arm64":
		return "linux", "arm64", ".tar.gz", nil
	case runtime.GOOS == "darwin" && runtime.GOARCH == "amd64":
		return "darwin", "x64", ".tar.gz", nil
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		return "darwin", "arm64", ".tar.gz", nil
	default:
		return "", "", "", fmt.Errorf("无 nodejs.org 资产支持主机 %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

// extractZip 解压 zip 到 dest，拒绝 zip-slip（条目逃逸目标目录）。保留文件权限位。
func extractZip(archive, dest string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("打开 zip 失败: %w", err)
	}
	defer r.Close()
	cleanDest := filepath.Clean(dest)
	for _, f := range r.File {
		target := filepath.Join(dest, f.Name)
		if !isWithinDir(cleanDest, filepath.Clean(target)) {
			return fmt.Errorf("归档条目逃逸目标目录: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, f.Mode()&0o777|0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode()&0o777)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			_ = out.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			_ = rc.Close()
			_ = out.Close()
			return err
		}
		_ = rc.Close()
		_ = out.Close()
	}
	return nil
}

// findNodeRoot 扫描 staging 顶层目录，返回包含 node 二进制的那一个（资产解压后兜底定位）。
func findNodeRoot(staging string) string {
	entries, err := os.ReadDir(staging)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(staging, e.Name())
		if findManagedNodeBin(dir, "node") != "" {
			return dir
		}
	}
	return ""
}

// managedNodeDir 托管 Node 安装目录：~/.eleball-claw/tools/node。
func managedNodeDir() string {
	return filepath.Join(managedToolsDir(), "node")
}

// findManagedNodeBin 在安装目录下查找 node/npx 二进制。command 为 "node" 或 "npx"。
// Windows: node.exe / npx.cmd 在目录根；Unix: bin/node / bin/npx。
func findManagedNodeBin(installDir, command string) string {
	if installDir == "" {
		return ""
	}
	var candidates []string
	if command == "npx" {
		candidates = []string{
			filepath.Join(installDir, "npx.cmd"),
			filepath.Join(installDir, "bin", "npx"),
		}
	} else {
		candidates = []string{
			filepath.Join(installDir, "node.exe"),
			filepath.Join(installDir, "bin", "node"),
		}
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

// managedNodeBinDir 返回托管 node 的可执行目录（用于 spawn 时把 node 放上 PATH，
// 供 npx 脚本 shebang 找到 node）。
func managedNodeBinDir() string {
	dir := managedNodeDir()
	if runtime.GOOS == "windows" {
		return dir
	}
	return filepath.Join(dir, "bin")
}

// nodeVersionRE 匹配 "v20.15.0" / "20.15.0" 输出中的版本号。
var nodeVersionRE = regexp.MustCompile(`v?(\d+\.\d+\.\d+)`)

// parseNodeVersion 解析 "v20.15.0" 为版本三元组（复用 pythonVersion）。
func parseNodeVersion(s string) (pythonVersion, bool) {
	s = strings.TrimSpace(s)
	if m := nodeVersionRE.FindStringSubmatch(s); m != nil {
		return parsePythonVersion(m[1])
	}
	return pythonVersion{}, false
}

// probeNodeVersion 运行 "<path> --version" 解析 node 版本字符串。5s 超时。
func probeNodeVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if m := nodeVersionRE.FindStringSubmatch(string(out)); m != nil {
		return m[1], nil
	}
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("无法解析 Node 版本: %q", string(out))
}

// isNodeCommand 判断命令是否为 node 家族。
func isNodeCommand(command string) bool {
	switch command {
	case "node", "npx":
		return true
	}
	return false
}

// resolveExpectedSHA256 优先从 SHA256SUMS 文件取校验值；缺失时回退 API digest；
// 两者皆无则拒绝安装（H1 下载信任要求强制校验）。
func (b *InterpreterBootstrap) resolveExpectedSHA256(ctx context.Context, assets []githubAsset, dist *pythonDistribution) (string, error) {
	sums, err := b.fetchSHA256Sums(ctx, assets)
	if err != nil {
		b.logger.Warn("获取 SHA256SUMS 失败，回退 API digest", zap.Error(err))
	}
	if expected, ok := sums[dist.assetName]; ok {
		return expected, nil
	}
	if d := strings.TrimPrefix(dist.digest, "sha256:"); d != "" {
		return d, nil
	}
	return "", fmt.Errorf("缺少 %s 的 SHA-256 校验值，拒绝安装", dist.assetName)
}

// fetchLatestRelease 取 latest 发布元数据。
func (b *InterpreterBootstrap) fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pythonBuildStandaloneReleasesAPI+"/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", downloadUserAgent)
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API 状态 %d", resp.StatusCode)
	}
	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	if len(rel.Assets) == 0 {
		return nil, errors.New("发布无资产")
	}
	return &rel, nil
}

// fetchSHA256Sums 下载 SHA256SUMS 资产并解析为 map[assetName]sha256。
func (b *InterpreterBootstrap) fetchSHA256Sums(ctx context.Context, assets []githubAsset) (map[string]string, error) {
	var sumsURL string
	for _, a := range assets {
		if a.Name == sha256SumsAssetName {
			sumsURL = a.BrowserDownloadURL
			break
		}
	}
	if sumsURL == "" {
		return nil, errors.New("发布未携带 SHA256SUMS 资产")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", downloadUserAgent)
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SHA256SUMS 状态 %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseSHA256Sums(string(body)), nil
}

// parseSHA256Sums 解析 SHA256SUMS 文本：每行 "<sha256>  <filename>"。
func parseSHA256Sums(content string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// 行格式：<hash>  <filename>（hash 与 filename 间为两空格或若干空白）
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hash := fields[0]
		name := strings.Join(fields[1:], " ")
		// filename 可能含空格被 Fields 拆分；python-build-standalone 资产名无空格，取最后一个字段更稳
		if len(fields) > 2 {
			name = fields[len(fields)-1]
		}
		if len(hash) == 64 && isHex(hash) {
			out[name] = hash
		}
	}
	return out
}

// downloadAndVerify 下载 url 到 dest，流式计算 SHA-256 并与 expected 比对。
func downloadAndVerify(ctx context.Context, client *http.Client, url, expected, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", downloadUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载状态 %d: %s", resp.StatusCode, url)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, hasher), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return closeErr
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		_ = os.Remove(dest)
		return fmt.Errorf("SHA-256 校验失败（期望 %s，实际 %s）", expected, actual)
	}
	return nil
}

// extractTarGz 解压 tar.gz 到 dest，保留文件权限，拒绝 zip-slip 与符号链接逃逸。
// 符号链接按目标名创建（best-effort，Windows 无权限时忽略——核心二进制不依赖符号链接）。
func extractTarGz(ctx context.Context, src io.Reader, dest string) error {
	gz, err := gzip.NewReader(src)
	if err != nil {
		return fmt.Errorf("gzip 头无效: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	cleanDest := filepath.Clean(dest)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, hdr.Name)
		// zip-slip 防护：解压目标必须在 dest 内。
		if !isWithinDir(cleanDest, filepath.Clean(target)) {
			return fmt.Errorf("归档条目逃逸目标目录: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0o777|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			_ = f.Close()
		case tar.TypeSymlink:
			// 符号链接目标也须限制在 dest 内（防符号链接逃逸）。
			linkTarget := filepath.Join(filepath.Dir(target), hdr.Linkname)
			if !isWithinDir(cleanDest, filepath.Clean(linkTarget)) {
				// 跨目录符号链接（如 python3 -> python3.12 同目录内相对链接）经 Join 后在 dest 内则放行；
				// 仅当解析后仍逃逸才拒绝。
				if filepath.IsAbs(hdr.Linkname) {
					return fmt.Errorf("归档含绝对路径符号链接: %s -> %s", hdr.Name, hdr.Linkname)
				}
			}
			_ = os.Symlink(hdr.Linkname, target) // best-effort：Windows 无权限时忽略
		case tar.TypeLink:
			_ = os.Link(filepath.Join(dest, hdr.Linkname), target) // best-effort
		}
	}
	return nil
}

// isWithinDir 判断 path 是否在 dir 目录内（或等于 dir）。
func isWithinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// findPythonRoot 扫描 staging 顶层目录，返回包含 python 二进制的那一个（兜底）。
func findPythonRoot(staging string) string {
	entries, err := os.ReadDir(staging)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(staging, e.Name())
		if findManagedPythonBin(dir) != "" {
			return dir
		}
	}
	return ""
}

// --- 托管路径与廉价磁盘探测（供 locateCommand 回退，无状态、不联网）---

// managedToolsDir 托管解释器根目录：~/.eleball-claw/tools。
func managedToolsDir() string {
	return filepath.Join(clawHomeDir(), ".eleball-claw", "tools")
}

// managedPythonDir 托管 Python 安装目录：~/.eleball-claw/tools/python。
func managedPythonDir() string {
	return filepath.Join(managedToolsDir(), "python")
}

// managedInterpreterPath 廉价磁盘探测：命令对应的托管解释器二进制路径。
// 已安装则返回绝对路径，否则返回空串。不触发网络下载。
// 供 locateCommand 在系统未找到时回退；支持 python 家族与 node/npx。
func managedInterpreterPath(command string) string {
	if isPythonCommand(command) {
		return findManagedPythonBin(managedPythonDir())
	}
	if isNodeCommand(command) {
		return findManagedNodeBin(managedNodeDir(), command)
	}
	return ""
}

// findManagedPythonBin 在安装目录下查找 python 二进制（Windows: python.exe；
// Unix: bin/python3.12 / bin/python3）。返回第一个存在的绝对路径。
func findManagedPythonBin(installDir string) string {
	if installDir == "" {
		return ""
	}
	candidates := []string{
		filepath.Join(installDir, "python.exe"),
		filepath.Join(installDir, "python3.exe"),
		filepath.Join(installDir, "python3.12.exe"),
		filepath.Join(installDir, "bin", "python3.12"),
		filepath.Join(installDir, "bin", "python3"),
		filepath.Join(installDir, "bin", "python"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

// clawHomeDir 用户主目录（Windows: USERPROFILE；Unix: HOME）。
func clawHomeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	if runtime.GOOS == "windows" {
		if h := os.Getenv("USERPROFILE"); h != "" {
			return h
		}
	}
	return ""
}

// --- 版本解析与发行版选择 ---

type pythonVersion struct{ major, minor, patch int }

func (v pythonVersion) String() string {
	return strconv.Itoa(v.major) + "." + strconv.Itoa(v.minor) + "." + strconv.Itoa(v.patch)
}

func (v pythonVersion) gte(o pythonVersion) bool {
	return v.major > o.major ||
		(v.major == o.major && v.minor > o.minor) ||
		(v.major == o.major && v.minor == o.minor && v.patch >= o.patch)
}

func (v pythonVersion) lt(o pythonVersion) bool {
	return !v.gte(o)
}

// pythonVersionRE 匹配 "Python 3.13.3" 输出中的版本号。
var pythonVersionRE = regexp.MustCompile(`Python (\d+\.\d+\.\d+)`)

// parsePythonVersion 解析 "3.13.3"、"3.14.0b3"、"3.12.0+20250409" 等为版本三元组。
// 预发布后缀（b3/rc1/a1）与构建元数据（+...）被截断——用于版本区间筛选，不精确到预发布。
func parsePythonVersion(s string) (pythonVersion, bool) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	// 截断预发布后缀：首个非 [0-9.] 字符。
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '.' && (c < '0' || c > '9') {
			s = s[:i]
			break
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return pythonVersion{}, false
	}
	var p [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return pythonVersion{}, false
		}
		p[i] = n
	}
	return pythonVersion{p[0], p[1], p[2]}, true
}

// isPythonCommand 判断命令是否为 python 家族。
func isPythonCommand(command string) bool {
	switch command {
	case "python", "python3", "python3.10", "python3.11", "python3.12", "python3.13":
		return true
	}
	return false
}

// probeCommandVersion 运行 "<path> --version" 解析版本字符串。5s 超时。
func probeCommandVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	out, err := cmd.CombinedOutput()
	if m := pythonVersionRE.FindStringSubmatch(string(out)); m != nil {
		return m[1], nil
	}
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("无法解析 Python 版本: %q", string(out))
}

// hostAssetSuffix 返回当前主机的 install_only 资产后缀。
func hostAssetSuffix() (string, error) {
	os := runtime.GOOS
	arch := runtime.GOARCH
	switch {
	case os == "windows" && arch == "amd64":
		return "x86_64-pc-windows-msvc-install_only.tar.gz", nil
	case os == "windows" && arch == "arm64":
		return "aarch64-pc-windows-msvc-install_only.tar.gz", nil
	case os == "linux" && arch == "amd64":
		return "x86_64-unknown-linux-gnu-install_only.tar.gz", nil
	case os == "linux" && arch == "arm64":
		return "aarch64-unknown-linux-gnu-install_only.tar.gz", nil
	case os == "darwin" && arch == "amd64":
		return "x86_64-apple-darwin-install_only.tar.gz", nil
	case os == "darwin" && arch == "arm64":
		return "aarch64-apple-darwin-install_only.tar.gz", nil
	default:
		return "", fmt.Errorf("无 python-build-standalone 资产支持主机 %s/%s", os, arch)
	}
}

// selectPythonDistribution 从发布资产中选主机兼容、版本区间内的最高版本资产，
// 优先 install_only_stripped。
func selectPythonDistribution(assets []githubAsset) (*pythonDistribution, error) {
	suffix, err := hostAssetSuffix()
	if err != nil {
		return nil, err
	}
	min, ok := parsePythonVersion(managedPythonMinVersion)
	if !ok {
		return nil, errors.New("内部错误：managedPythonMinVersion 解析失败")
	}
	max, _ := parsePythonVersion(managedPythonMaxVersion)

	var candidates []*pythonDistribution
	for i := range assets {
		d, ok := parsePythonAsset(&assets[i])
		if !ok {
			continue
		}
		if !assetMatchesTarget(d.assetName, suffix) {
			continue
		}
		if !d.version.gte(min) || !d.version.lt(max) {
			continue
		}
		candidates = append(candidates, d)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("发布中无主机兼容且版本 %s..<%s 的 python-build-standalone 资产",
			managedPythonMinVersion, managedPythonMaxVersion)
	}
	// 版本降序，资产名升序（稳定）。
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].version != candidates[j].version {
			return !candidates[i].version.lt(candidates[j].version)
		}
		return candidates[i].assetName < candidates[j].assetName
	})
	// 优先 stripped 变体（体积更小）。
	for _, c := range candidates {
		if strings.Contains(c.assetName, "install_only_stripped") {
			return c, nil
		}
	}
	return candidates[0], nil
}

// parsePythonAsset 解析 cpython-X.Y.Z+...-install_only[_stripped].tar.gz 资产。
func parsePythonAsset(a *githubAsset) (*pythonDistribution, bool) {
	name := a.Name
	if !strings.HasPrefix(name, "cpython-") || !strings.HasSuffix(name, ".tar.gz") || !strings.Contains(name, "install_only") {
		return nil, false
	}
	rest := strings.TrimPrefix(name, "cpython-")
	verStr := rest
	if i := strings.IndexByte(rest, '+'); i >= 0 {
		verStr = rest[:i]
	}
	ver, ok := parsePythonVersion(verStr)
	if !ok {
		return nil, false
	}
	return &pythonDistribution{
		releaseTag: "",
		assetName:  name,
		url:        a.BrowserDownloadURL,
		version:    ver,
		digest:     a.Digest,
	}, true
}

// assetMatchesTarget 资产名匹配主机后缀（install_only 或 install_only_stripped 变体）。
func assetMatchesTarget(assetName, targetSuffix string) bool {
	if strings.HasSuffix(assetName, targetSuffix) {
		return true
	}
	stripped := strings.Replace(targetSuffix, "-install_only.tar.gz", "-install_only_stripped.tar.gz", 1)
	return strings.HasSuffix(assetName, stripped)
}

// isHex 判断字符串是否为纯十六进制。
func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
