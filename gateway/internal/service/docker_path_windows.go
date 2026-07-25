//go:build windows

package service

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// dockerFallbackDirs Windows 下 docker.exe 的候选目录：
// 1) Docker Desktop 默认安装目录（ProgramFiles\Docker\Docker\resources\bin）
// 2) 注册表中的系统/用户 Path（覆盖自定义安装位置；注册表在安装时即写入，
//    即使当前进程 PATH 未刷新也能找到）
func dockerFallbackDirs() []string {
	var dirs []string
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		dirs = append(dirs, filepath.Join(pf, "Docker", "Docker", "resources", "bin"))
	}
	// 用户级 shim 目录：WSL 桥接 shim（docker.bat）等手动安装的工具常放于此
	if up := os.Getenv("USERPROFILE"); up != "" {
		dirs = append(dirs, filepath.Join(up, "bin"))
	}
	dirs = append(dirs, registryPathDirs()...)
	return dedupeDirs(dirs)
}

// winEnvVarPattern 匹配 Windows 注册表 Path 中的 %VAR% 形式环境变量
var winEnvVarPattern = regexp.MustCompile(`%([^%]+)%`)

// registryPathDirs 读取注册表中的系统与用户 Path 并展开 %VAR% 环境变量。
// 注册表值通常为 REG_EXPAND_SZ，os.ExpandEnv 只处理 $VAR/${VAR}，需自行展开。
func registryPathDirs() []string {
	var dirs []string
	keys := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
		{registry.CURRENT_USER, `Environment`},
	}
	for _, k := range keys {
		key, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		val, _, err := key.GetStringValue("Path")
		key.Close()
		if err != nil {
			continue
		}
		val = winEnvVarPattern.ReplaceAllStringFunc(val, func(m string) string {
			if v := os.Getenv(m[1 : len(m)-1]); v != "" {
				return v
			}
			return m
		})
		for _, d := range strings.Split(val, ";") {
			if d = strings.TrimSpace(d); d != "" {
				dirs = append(dirs, d)
			}
		}
	}
	return dirs
}

// dedupeDirs 去重保序（Windows 路径大小写不敏感，按小写比较）
func dedupeDirs(dirs []string) []string {
	seen := make(map[string]bool, len(dirs))
	out := dirs[:0]
	for _, d := range dirs {
		k := strings.ToLower(d)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, d)
	}
	return out
}
