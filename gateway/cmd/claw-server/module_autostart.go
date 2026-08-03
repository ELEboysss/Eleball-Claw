package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/service"
	"go.uber.org/zap"
)

// 预置模块生命周期管理：serve 启动时自动上线、退出时自动下线。
//
// 与 `eleball-claw module up/down` 使用同一 compose project 命名（eleball-claw-<名称>），
// 因此与用户手动执行 module 命令幂等共存：重复 up 无副作用，down 只清理容器。
// 仅处理 marketplace home 下含 docker-compose.yml 的模块（官方预置 + 用户自开发）；
// 云端安装的第三方模块由 image_installer 独立管理，不在此范围。
//
// 上线策略（modules.pull_policy）：
//   - pull_first（默认）：先 docker pull <registry>/<module>:<image_tag>（与 CI 滚动标签一致），
//     成功后 compose up -d 复用本地已拉镜像；拉取失败回退 compose up -d --build 本地构建兜底；
//   - build_only：始终本地构建（旧行为）；
//   - pull_only：仅拉镜像，失败即标记失败，不回退构建。

// 镜像上线策略常量
const (
	pullPolicyPullFirst = "pull_first"
	pullPolicyBuildOnly = "build_only"
	pullPolicyPullOnly  = "pull_only"
)

// moduleUpTimeout 单模块拉镜像/首次构建镜像的最长等待；超时则放弃该模块（网关继续运行）
const moduleUpTimeout = 10 * time.Minute

// moduleDownTimeout 退出时单模块停止的最长等待
const moduleDownTimeout = 60 * time.Second

// moduleImageRef 拼接模块镜像引用：<registry>/<module>:<tag>（module 即 marketplace 目录名）
func moduleImageRef(registry, name, tag string) string {
	return strings.TrimSuffix(registry, "/") + "/" + name + ":" + tag
}

// normalizePullPolicy 归一化 pull_policy 配置值，空值/非法值回退默认 pull_first
func normalizePullPolicy(policy string) string {
	switch policy {
	case pullPolicyPullFirst, pullPolicyBuildOnly, pullPolicyPullOnly:
		return policy
	default:
		return pullPolicyPullFirst
	}
}

// autoStartModules 后台依次上线所有可启动模块（拉镜像优先，本地构建兜底）。
// 返回成功上线的模块名列表（供关闭时 down）。
// docker 缺失、marketplace 目录异常、单模块失败均只告警不阻断。
// ctx 取消时中断后续模块的启动（已启动的仍返回，便于退出时清理）。
func autoStartModules(ctx context.Context, logger *zap.Logger, cfg config.ModulesConfig, registry *service.SkillRuntimeRegistry) []string {
	if service.EnsureDockerOnPath() == "" {
		logger.Warn("未检测到 docker，跳过预置模块自动上线（模块将保持离线，可安装 Docker 后重启或手动 module up）")
		return nil
	}
	root, err := service.EnsureMarketplaceRoot()
	if err != nil || root == "" {
		logger.Warn("无法确定 marketplace 目录，跳过模块自动上线", zap.Error(err))
		return nil
	}
	absRoot, _ := filepath.Abs(root)
	targets, err := resolveModuleTargets(absRoot, nil)
	if err != nil {
		logger.Info("没有可自动启动的模块", zap.String("reason", err.Error()))
		return nil
	}

	policy := normalizePullPolicy(cfg.PullPolicy)
	var started []string
	for _, name := range targets {
		if ctx.Err() != nil {
			break
		}
		if err := startModule(ctx, logger, absRoot, name, cfg, policy); err != nil {
			logger.Warn("模块自动上线失败（网关继续运行，可手动 module up 排查）",
				zap.String("module", name), zap.Error(err))
			continue
		}
		started = append(started, name)
		logger.Info("模块已上线，等待健康探测转在线", zap.String("module", name))
	}

	// 容器就绪需要几秒；逐模块短重试强制探测，让状态尽快转在线，
	// 否则要等待后台探测周期（默认 5 分钟），技能页会长时间误显示离线。
	if registry != nil {
		for _, name := range started {
			for attempt := 0; attempt < 10; attempt++ {
				if ctx.Err() != nil {
					break
				}
				time.Sleep(3 * time.Second)
				if st := registry.ForceProbe(name); st != nil && st.Online {
					break
				}
			}
		}
	}
	return started
}

// startModule 按策略上线单个模块：拉镜像优先 / 仅构建 / 仅拉取。
func startModule(ctx context.Context, logger *zap.Logger, absRoot, name string, cfg config.ModulesConfig, policy string) error {
	if policy != pullPolicyBuildOnly {
		ref := moduleImageRef(cfg.Registry, name, cfg.ImageTag)
		logger.Info("自动上线模块：拉取镜像", zap.String("module", name), zap.String("image", ref))
		if err := dockerPull(ctx, name, ref, moduleUpTimeout); err != nil {
			if policy == pullPolicyPullOnly {
				return fmt.Errorf("拉取镜像失败（pull_only 不回退本地构建）: %w", err)
			}
			logger.Warn("拉取镜像失败，回退本地构建", zap.String("module", name), zap.String("image", ref), zap.Error(err))
		} else {
			// 拉取成功：compose up 复用本地已拉镜像（compose 文件 image 字段与该 ref 对齐）
			if err := composeRun(ctx, absRoot, name, moduleUpTimeout, "up", "-d"); err != nil {
				return err
			}
			logger.Info("模块已拉取上线", zap.String("module", name), zap.String("image", ref))
			return nil
		}
	}
	// build_only 或 pull_first 拉取失败回退：本地构建
	logger.Info("自动上线模块：本地构建（首次构建可能需要几分钟）", zap.String("module", name))
	if err := composeRun(ctx, absRoot, name, moduleUpTimeout, "up", "-d", "--build"); err != nil {
		return err
	}
	logger.Info("模块已构建上线", zap.String("module", name))
	return nil
}

// autoStopModules 退出时 docker compose down 指定模块（仅调用方传入的、本网关启动成功的模块）。
func autoStopModules(logger *zap.Logger, names []string) {
	if len(names) == 0 {
		return
	}
	if service.EnsureDockerOnPath() == "" {
		return
	}
	root, err := service.EnsureMarketplaceRoot()
	if err != nil || root == "" {
		return
	}
	absRoot, _ := filepath.Abs(root)
	for _, name := range names {
		logger.Info("自动下线模块", zap.String("module", name))
		ctx, cancel := context.WithTimeout(context.Background(), moduleDownTimeout)
		if err := composeRun(ctx, absRoot, name, moduleDownTimeout, "down"); err != nil {
			logger.Warn("模块自动下线失败", zap.String("module", name), zap.Error(err))
		}
		cancel()
	}
}

// composeRun 执行 docker compose -f <compose> -p eleball-claw-<name> <args...>
func composeRun(ctx context.Context, root, name string, timeout time.Duration, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	compose := filepath.Join(root, name, "docker-compose.yml")
	dargs := append([]string{"compose", "-f", compose, "-p", "eleball-claw-" + name}, args...)
	cmd := service.DockerCommand(ctx, dargs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &composeError{module: name, output: string(out), err: err}
	}
	return nil
}

// dockerPull 拉取模块镜像（docker pull <ref>），超时/失败由调用方按策略回退或报错
func dockerPull(ctx context.Context, name, ref string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := service.DockerCommand(ctx, "pull", ref)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &composeError{module: name, output: string(out), err: err}
	}
	return nil
}

type composeError struct {
	module string
	output string
	err    error
}

func (e *composeError) Error() string {
	return e.err.Error() + ": " + e.output
}
