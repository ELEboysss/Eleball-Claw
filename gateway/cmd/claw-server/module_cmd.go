package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eleball/gateway/internal/service"
)

// module 子命令：安装版 claw 的模块管理入口。
//
// marketplace home（默认 ~/.eleball-claw/marketplace，首次使用自动播种官方模块）
// 是用户开发/管理模块的目录；每个含 docker-compose.yml 的子目录即一个可启动模块。
//
// 用法：
//   eleball-claw module ls                 列出模块与运行状态
//   eleball-claw module up [名称...]       构建并后台启动（缺省全部）
//   eleball-claw module down [名称...]     停止并移除（缺省全部）
//   eleball-claw module ps [名称...]       查看容器状态
//   eleball-claw module logs <名称> [-f]   查看日志（-f 持续跟踪）
func runModuleCommand(args []string) int {
	if len(args) == 0 {
		printModuleUsage()
		return 1
	}

	root, err := service.EnsureMarketplaceRoot()
	if err != nil || root == "" {
		fmt.Fprintf(os.Stderr, "无法确定 marketplace 目录: %v\n", err)
		return 1
	}
	absRoot, _ := filepath.Abs(root)

	cmd := args[0]
	rest := args[1:]

	if cmd == "ls" {
		return moduleList(absRoot)
	}

	// up/down/ps/logs 需要 docker
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Fprintln(os.Stderr, "未找到 docker 命令。模块以容器方式运行，请先安装 Docker（https://docs.docker.com/get-docker/）")
		return 1
	}

	// logs 的参数可能带 -f 等 flag，模块名取第一个非 flag 参数
	follow := false
	var names []string
	for _, a := range rest {
		if a == "-f" || a == "--follow" {
			follow = true
			continue
		}
		names = append(names, a)
	}

	targets, err := resolveModuleTargets(absRoot, names)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	rc := 0
	for _, name := range targets {
		compose := filepath.Join(absRoot, name, "docker-compose.yml")
		project := "eleball-claw-" + name
		var dargs []string
		switch cmd {
		case "up":
			fmt.Printf("==> 启动模块 %s（首次构建可能需要几分钟）\n", name)
			dargs = []string{"compose", "-f", compose, "-p", project, "up", "-d", "--build"}
		case "down":
			fmt.Printf("==> 停止模块 %s\n", name)
			dargs = []string{"compose", "-f", compose, "-p", project, "down"}
		case "ps":
			dargs = []string{"compose", "-f", compose, "-p", project, "ps", "-a"}
		case "logs":
			dargs = []string{"compose", "-f", compose, "-p", project, "logs", "--tail=200"}
			if follow {
				dargs = append(dargs, "-f")
			}
		default:
			printModuleUsage()
			return 1
		}
		c := exec.Command("docker", dargs...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		c.Stdin = os.Stdin
		if err := c.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "模块 %s 执行 %s 失败: %v\n", name, cmd, err)
			rc = 1
		}
	}

	if cmd == "up" && rc == 0 {
		fmt.Println("提示：模块启动后约 1 分钟内网关健康探测会自动转为在线，可在控制台「本地模块」页查看。")
	}
	return rc
}

// moduleList 列出 marketplace home 下的模块（含 module.json 摘要与 compose 可用性）
func moduleList(root string) int {
	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取 marketplace 目录失败: %v\n", err)
		return 1
	}
	fmt.Printf("marketplace 目录: %s（在该目录下开发/管理模块，重启或控制台「重新扫描」后生效）\n\n", root)
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta := filepath.Join(root, e.Name(), "module.json")
		data, err := os.ReadFile(meta)
		if err != nil {
			continue
		}
		var m struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
			URL          string `json:"url"`
			Capabilities []string `json:"capabilities"`
		}
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		compose := "无 compose（仅登记）"
		if _, err := os.Stat(filepath.Join(root, e.Name(), "docker-compose.yml")); err == nil {
			compose = "可 docker 启动"
		}
		fmt.Printf("  %-14s %s\n                %s\n                URL: %s | 能力: %s | %s\n",
			e.Name(), m.Name, m.Description, m.URL, strings.Join(m.Capabilities, ","), compose)
		count++
	}
	if count == 0 {
		fmt.Println("  （空）把含 module.json 的模块目录放到这里即可被扫描登记。")
	}
	return 0
}

// resolveModuleTargets 解析目标模块：缺省为所有含 docker-compose.yml 的模块
func resolveModuleTargets(root string, names []string) ([]string, error) {
	available := map[string]bool{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("读取 marketplace 目录失败: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), "docker-compose.yml")); err == nil {
			available[e.Name()] = true
		}
	}
	if len(names) == 0 {
		var all []string
		for name := range available {
			all = append(all, name)
		}
		if len(all) == 0 {
			return nil, fmt.Errorf("marketplace 目录 %s 下没有可用 docker compose 启动的模块", root)
		}
		sort.Strings(all)
		return all, nil
	}
	for _, n := range names {
		if !available[n] {
			return nil, fmt.Errorf("模块 %s 不存在或不含 docker-compose.yml（module ls 查看可用模块）", n)
		}
	}
	return names, nil
}

func printModuleUsage() {
	fmt.Println(`用法: eleball-claw module <命令> [模块名...]

  ls                 列出 marketplace 目录下的模块
  up [名称...]       构建并后台启动模块（缺省全部）
  down [名称...]     停止并移除模块容器（缺省全部）
  ps [名称...]       查看模块容器状态
  logs <名称> [-f]   查看模块日志（-f 持续跟踪）

marketplace 目录默认 ~/.eleball-claw/marketplace（CLAW_MARKETPLACE_DIR 可覆盖），
首次使用自动播种官方模块；自定义模块放入该目录即可被扫描登记。`)
}
