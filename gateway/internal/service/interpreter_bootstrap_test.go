package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParsePythonVersion(t *testing.T) {
	cases := []struct {
		in   string
		want pythonVersion
		ok   bool
	}{
		{"3.13.3", pythonVersion{3, 13, 3}, true},
		{"3.12.0", pythonVersion{3, 12, 0}, true},
		{"3.14.0b3", pythonVersion{3, 14, 0}, true},        // 预发布截断
		{"3.13.1+20250409", pythonVersion{3, 13, 1}, true}, // 构建元数据截断
		{"3.13", pythonVersion{3, 13, 0}, true},            // 缺 patch 补 0
		{"3", pythonVersion{}, false},
		{"", pythonVersion{}, false},
		{"abc", pythonVersion{}, false},
	}
	for _, c := range cases {
		got, ok := parsePythonVersion(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parsePythonVersion(%q) = %v,%v; want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestPythonVersionCmp(t *testing.T) {
	a, _ := parsePythonVersion("3.12.0")
	b, _ := parsePythonVersion("3.13.3")
	c, _ := parsePythonVersion("3.14.0")
	if !b.gte(a) {
		t.Error("3.13.3 应 >= 3.12.0")
	}
	if a.gte(b) {
		t.Error("3.12.0 不应 >= 3.13.3")
	}
	if !c.lt(parsePythonVersionMust("3.15.0")) {
		t.Error("3.14.0 应 < 3.15.0")
	}
	if c.lt(parsePythonVersionMust("3.14.0")) {
		// 等于上界：lt 应为 false（上界排他，等于不算小于）
		t.Error("3.14.0 lt 3.14.0 应为 false")
	}
	if !b.lt(c) {
		t.Error("3.13.3 应 < 3.14.0")
	}
}

func parsePythonVersionMust(s string) pythonVersion {
	v, ok := parsePythonVersion(s)
	if !ok {
		panic("parse fail: " + s)
	}
	return v
}

func TestSelectPythonDistribution(t *testing.T) {
	// 构造一份覆盖多版本/多平台/ stripped 与否的假资产。
	assets := []githubAsset{
		{Name: "cpython-3.11.9+20250101-x86_64-unknown-linux-gnu-install_only.tar.gz", BrowserDownloadURL: "u-311"},
		{Name: "cpython-3.12.0+20250101-x86_64-pc-windows-msvc-install_only.tar.gz", BrowserDownloadURL: "u-312-win"},
		{Name: "cpython-3.13.3+20250101-x86_64-pc-windows-msvc-install_only.tar.gz", BrowserDownloadURL: "u-313-win"},
		{Name: "cpython-3.13.3+20250101-x86_64-pc-windows-msvc-install_only_stripped.tar.gz", BrowserDownloadURL: "u-313-win-stripped"},
		{Name: "cpython-3.14.0b3+20250101-x86_64-pc-windows-msvc-install_only.tar.gz", BrowserDownloadURL: "u-314beta"},
		{Name: "cpython-3.13.3+20250101-aarch64-apple-darwin-install_only.tar.gz", BrowserDownloadURL: "u-313-mac-arm"},
		{Name: "README.md", BrowserDownloadURL: "u-readme"},
	}
	dist, err := selectPythonDistribution(assets)
	if err != nil {
		t.Fatalf("selectPythonDistribution 失败: %v", err)
	}
	// 期望命中 stripped（Windows amd64 测试机若非该平台则用其平台对应资产）。
	// 关键断言：版本 >= 3.12 且 < 3.14，且优先 stripped。
	if !dist.version.gte(parsePythonVersionMust("3.12.0")) {
		t.Errorf("版本 %s 应 >= 3.12.0", dist.version)
	}
	if !dist.version.lt(parsePythonVersionMust("3.14.0")) {
		t.Errorf("版本 %s 应 < 3.14.0", dist.version)
	}
	if !strings.Contains(dist.assetName, "install_only") {
		t.Errorf("资产名应含 install_only: %s", dist.assetName)
	}
}

func TestSelectPythonDistributionPrefersStripped(t *testing.T) {
	// 仅 Windows amd64 两个资产，验证 stripped 优先。
	assets := []githubAsset{
		{Name: "cpython-3.13.3+20250101-x86_64-pc-windows-msvc-install_only.tar.gz", BrowserDownloadURL: "u-full"},
		{Name: "cpython-3.13.3+20250101-x86_64-pc-windows-msvc-install_only_stripped.tar.gz", BrowserDownloadURL: "u-stripped"},
	}
	dist, err := selectPythonDistribution(assets)
	if err != nil {
		// 非 windows/amd64 机器会因后缀不匹配而无候选--跳过而非失败。
		if strings.Contains(err.Error(), "无主机兼容") {
			t.Skip("当前主机非 windows/amd64，跳过 stripped 偏好断言")
		}
		t.Fatalf("selectPythonDistribution 失败: %v", err)
	}
	if !strings.Contains(dist.assetName, "install_only_stripped") {
		t.Errorf("应优先 stripped 资产，实得 %s", dist.assetName)
	}
}

func TestParsePythonAsset(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"cpython-3.13.3+20250101-x86_64-pc-windows-msvc-install_only.tar.gz", true},
		{"cpython-3.13.3+20250101-x86_64-pc-windows-msvc-install_only_stripped.tar.gz", true},
		{"cpython-3.14.0b3+20250101-x86_64-pc-windows-msvc-install_only.tar.gz", true},
		{"README.md", false},
		{"cpython-3.13.3+20250101-x86_64-pc-windows-msvc-shared-install_only.tar.gz", true}, // 含 install_only 子串故 parse 通过，由 assetMatchesTarget 后缀匹配过滤
		{"cpython-3.13.3+20250101-x86_64-pc-windows-msvc-install_only.zip", false},          // 非 tar.gz
	}
	for _, c := range cases {
		_, ok := parsePythonAsset(&githubAsset{Name: c.name})
		if ok != c.ok {
			t.Errorf("parsePythonAsset(%q) ok=%v, want %v", c.name, ok, c.ok)
		}
	}
}

func TestAssetMatchesTarget(t *testing.T) {
	suffix := "x86_64-pc-windows-msvc-install_only.tar.gz"
	if !assetMatchesTarget("cpython-3.13.3+20250101-x86_64-pc-windows-msvc-install_only.tar.gz", suffix) {
		t.Error("install_only 变体应匹配")
	}
	if !assetMatchesTarget("cpython-3.13.3+20250101-x86_64-pc-windows-msvc-install_only_stripped.tar.gz", suffix) {
		t.Error("install_only_stripped 变体应匹配")
	}
	if assetMatchesTarget("cpython-3.13.3+20250101-aarch64-apple-darwin-install_only.tar.gz", suffix) {
		t.Error("其他平台不应匹配")
	}
}

func TestParseSHA256Sums(t *testing.T) {
	// 三个合法 64 位 hex 行 + 一个短 hash（应排除）+ 无效行。
	h64 := func(n byte) string {
		b := make([]byte, 64)
		for i := range b {
			b[i] = n
		}
		return string(b)
	}
	full := "cpython-3.13.3+20250101-x86_64-pc-windows-msvc-install_only.tar.gz"
	stripped := "cpython-3.13.3+20250101-x86_64-pc-windows-msvc-install_only_stripped.tar.gz"
	beta := "cpython-3.14.0b3+20250101-x86_64-pc-windows-msvc-install_only.tar.gz"
	content := strings.Join([]string{
		h64('1') + "  " + full,
		h64('2') + "  " + stripped,
		h64('3') + "  " + beta,
		"abc123  short-name.tar.gz", // 短 hash，应排除
		"invalidline",
		"",
	}, "\n")
	got := parseSHA256Sums(content)
	if len(got) != 3 {
		t.Fatalf("应解析 3 行，实得 %d: %v", len(got), got)
	}
	if got[full] != h64('1') {
		t.Errorf("full 资产 hash 错误: %s", got[full])
	}
	if _, ok := got[stripped]; !ok {
		t.Errorf("缺少 %s", stripped)
	}
}

func TestFindManagedPythonBin(t *testing.T) {
	dir := t.TempDir()
	// 初始空目录
	if findManagedPythonBin(dir) != "" {
		t.Error("空目录应返回空")
	}
	// 放一个 bin/python3.12（unix 布局）
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binDir, "python3.12")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := findManagedPythonBin(dir)
	if got != bin {
		t.Errorf("应找到 %s，实得 %s", bin, got)
	}
}

func TestManagedInterpreterPath(t *testing.T) {
	// node/npx 暂不支持托管，应返回空。
	if managedInterpreterPath("npx") != "" {
		t.Error("npx 不应支持托管")
	}
	if managedInterpreterPath("node") != "" {
		t.Error("node 不应支持托管")
	}
	// python 在未安装时应返回空（不报错、不联网）。
	if managedInterpreterPath("python") != "" {
		t.Error("未安装时 python 应返回空")
	}
}

func TestIsPythonCommand(t *testing.T) {
	for _, c := range []string{"python", "python3", "python3.12"} {
		if !isPythonCommand(c) {
			t.Errorf("%s 应为 python 命令", c)
		}
	}
	for _, c := range []string{"npx", "node", "uv", ""} {
		if isPythonCommand(c) {
			t.Errorf("%s 不应为 python 命令", c)
		}
	}
}

// TestExtractTarGzZipSlip 构造含逃逸路径的 tar，断言被拒绝。
func TestExtractTarGzZipSlip(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// 恶意条目：../escape.txt
	_ = tw.WriteHeader(&tar.Header{Name: "../escape.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 3})
	_, _ = tw.Write([]byte("bad"))
	_ = tw.Close()
	_ = gz.Close()

	dest := t.TempDir()
	err := extractTarGz(context.Background(), bytes.NewReader(buf.Bytes()), dest)
	if err == nil {
		t.Fatal("zip-slip 归档应被拒绝")
	}
	// 确认逃逸文件未写出。
	if _, err := os.Stat(filepath.Join(dest, "..", "escape.txt")); err == nil {
		t.Error("逃逸文件不应被写出")
	}
}

// TestExtractTarGzNormal 正常归档解压（含子目录与可执行文件）。
func TestExtractTarGzNormal(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// 模拟 install_only 顶层 python/ 布局
	_ = tw.WriteHeader(&tar.Header{Name: "python/", Typeflag: tar.TypeDir, Mode: 0o755})
	_ = tw.WriteHeader(&tar.Header{Name: "python/bin/", Typeflag: tar.TypeDir, Mode: 0o755})
	_ = tw.WriteHeader(&tar.Header{Name: "python/bin/python3.12", Typeflag: tar.TypeReg, Mode: 0o755, Size: 5})
	_, _ = tw.Write([]byte("dummy"))
	_ = tw.Close()
	_ = gz.Close()

	dest := t.TempDir()
	if err := extractTarGz(context.Background(), bytes.NewReader(buf.Bytes()), dest); err != nil {
		t.Fatalf("正常归档解压失败: %v", err)
	}
	bin := filepath.Join(dest, "python", "bin", "python3.12")
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("解压后二进制应存在: %v", err)
	}
	// 可执行位仅在 Unix 有意义（Windows 无可执行位，python.exe 按扩展名执行）。
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		t.Error("可执行文件应保留可执行位")
	}
	// findPythonRoot 应找到 python/
	root := findPythonRoot(dest)
	if root != filepath.Join(dest, "python") {
		t.Errorf("findPythonRoot = %s, want %s", root, filepath.Join(dest, "python"))
	}
}

func TestIsWithinDir(t *testing.T) {
	dest := filepath.Clean("/tmp/extract")
	if !isWithinDir(dest, filepath.Clean("/tmp/extract/a/b")) {
		t.Error("子路径应在内")
	}
	if !isWithinDir(dest, dest) {
		t.Error("自身应在内")
	}
	if isWithinDir(dest, filepath.Clean("/tmp/escape")) {
		t.Error("同级逃逸应判定为外")
	}
	if isWithinDir(dest, filepath.Clean("/tmp/extract-evil")) {
		t.Error("前缀碰撞应判定为外")
	}
}
