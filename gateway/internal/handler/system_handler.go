package handler

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// SystemHandler claw 本地系统状态处理器。
//
// 提供 docker / compose 可用性与预置模块自动上下线开关的状态查询，
// 供本地控制台判断模块自动上线能力（无 docker 时引导安装或手动 module up）。
type SystemHandler struct {
	modules config.ModulesConfig
}

// NewSystemHandler 创建系统状态处理器（modules 配置快照随启动注入）
func NewSystemHandler(modules config.ModulesConfig) *SystemHandler {
	return &SystemHandler{modules: modules}
}

// statusProbeTimeout docker 状态探测的单次超时。
// 经 shim（如 WSL 桥接）调用时可能触发 dockerd 冷启动（等待就绪最长约 40s），
// 超时需覆盖该场景；daemon 已在运行时探测为毫秒级，不影响正常路径耗时。
const statusProbeTimeout = 45 * time.Second

// GetSystemStatus 系统状态（docker/compose 可用性 + 模块自动上下线开关）。
// GET /v1/claw-console/system/status
func (h *SystemHandler) GetSystemStatus(c *gin.Context) {
	// PATH 未刷新场景（Windows 安装 Docker 后未重开终端）先回退探测常见安装位置
	service.EnsureDockerOnPath()
	dockerVersion := probeDockerVersion()
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"docker_available":   dockerVersion != "",
			"docker_version":     dockerVersion,
			"compose_available":  probeComposeAvailable(),
			"modules_auto_start": h.modules.AutoStart,
			"modules_auto_stop":  h.modules.AutoStop,
		},
	})
}

// probeDockerVersion 查询 docker server 版本；失败（未安装/守护进程未运行/超时）返回空串
func probeDockerVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), statusProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// probeComposeAvailable 探测 docker compose 插件是否可用
func probeComposeAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), statusProbeTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "docker", "compose", "version").Run() == nil
}
