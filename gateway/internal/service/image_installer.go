package service

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
)

// ImageInstaller 第三方模块容器镜像安装器。
//
// P4：claw 技能页「安装到本地」对云端已购的第三方模块，按 ModuleInstallMeta.image
// 拉取容器镜像、校验签名、启动容器、返回可注册到本地 registry 的 ModuleRecord。
//
// 运行时依赖：本地容器运行时（docker 或 podman）。官方预置模块免镜像，不经此安装器。
// 安全：仅拉可信源 + digest 内容寻址 + 签名校验（cosign），校验失败拒绝激活。
// 详见 docs/marketing/claw-implementation-plan.md §F.2。
type ImageInstaller struct {
	// runtime 容器运行时命令名：docker / podman。运行时探测，nil 则自动选可用项。
	runtime string
}

// NewImageInstaller 创建镜像安装器。runtime 为空时按 docker->podman 顺序探测可用运行时。
func NewImageInstaller(runtime string) *ImageInstaller {
	if runtime == "" {
		// PATH 未刷新场景（Windows 安装 Docker 后未重开终端）回退探测常见安装位置
		EnsureDockerOnPath()
		if _, err := exec.LookPath("docker"); err == nil {
			runtime = "docker"
		} else if _, err := exec.LookPath("podman"); err == nil {
			runtime = "podman"
		} else {
			runtime = "" // 无可用运行时，安装第三方模块时返回明确错误
		}
	}
	return &ImageInstaller{runtime: runtime}
}

// Runtime 返回当前使用的容器运行时（docker/podman），空表示未检测到。
func (i *ImageInstaller) Runtime() string { return i.runtime }

// pullImage 拉取镜像。优先用 digest 内容寻址（imageRef 已含 @sha256:...）。
func (i *ImageInstaller) pullImage(ctx context.Context, imageRef string) error {
	if i.runtime == "" {
		return fmt.Errorf("未检测到容器运行时（docker/podman），无法拉取第三方模块镜像；请先安装 Docker 或 Podman")
	}
	cmd := exec.CommandContext(ctx, i.runtime, "pull", imageRef)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("拉取镜像失败 (%s pull %s): %v\n%s", i.runtime, imageRef, err, string(out))
	}
	return nil
}

// verifySignature 校验镜像签名（cosign）。签名非空时必须校验通过，否则拒绝激活（防篡改）。
// 未安装 cosign 或签名校验失败均视为不通过。
func (i *ImageInstaller) verifySignature(ctx context.Context, imageRef, signature string) error {
	if signature == "" {
		// 无签名：云端下发但缺签名，拒绝（第三方模块必须签名）
		return fmt.Errorf("第三方模块镜像缺少签名，拒绝激活")
	}
	if _, err := exec.LookPath("cosign"); err != nil {
		return fmt.Errorf("未安装 cosign，无法校验镜像签名；请安装 cosign 后再安装第三方模块")
	}
	// cosign verify --certificate-identity / --certificate-oidc-issuer 依部署而定。
	// 此处先做基础 verify（本地公钥/默认身份），生产应配置可信身份与 issuer。
	cmd := exec.CommandContext(ctx, "cosign", "verify", imageRef, "--certificate-identity", "https://github.com/Eleball/*", "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("镜像签名校验失败: %v\n%s", err, string(out))
	}
	return nil
}

// startContainer 启动容器并返回容器内服务地址（host:port）。
// 端口映射：容器内模块端口固定 8080 -> 宿主机动态分配端口。
func (i *ImageInstaller) startContainer(ctx context.Context, moduleID, imageRef string, envVars map[string]string) (string, error) {
	if i.runtime == "" {
		return "", fmt.Errorf("未检测到容器运行时，无法启动模块容器")
	}
	containerName := "eleball-module-" + moduleID
	args := []string{
		"run", "-d", "--rm",
		"--name", containerName,
		"-p", "8080", // 容器 8080 -> 宿主机随机端口
		"-l", "eleball.module=" + moduleID,
	}
	for k, v := range envVars {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, imageRef)

	cmd := exec.CommandContext(ctx, i.runtime, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("启动容器失败 (%s): %v\n%s", i.runtime, err, string(out))
	}

	// 查询端口映射：docker port <name> 8080 -> "0.0.0.0:32768"
	portCmd := exec.CommandContext(ctx, i.runtime, "port", containerName, "8080")
	portOut, err := portCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("查询容器端口失败: %v\n%s", err, string(portOut))
	}
	// 输出形如 "0.0.0.0:32768\n" 或 "::1:32768\n"
	addr := strings.TrimSpace(string(portOut))
	// 取最后一行，规范化为 127.0.0.1:port（claw 本地访问容器）
	parts := strings.Split(addr, ":")
	if len(parts) < 2 {
		return "", fmt.Errorf("无法解析容器端口映射: %s", addr)
	}
	port := parts[len(parts)-1]
	return "http://127.0.0.1:" + port, nil
}

// Install 拉取并校验镜像、启动容器，返回构造好的 ModuleRecord（未注册到 registry）。
// 调用方（ModuleService）负责把返回的 record 写入 registry 激活。
//
// official=true 的模块不经此安装器（直接激活预置）；此处仅处理第三方。
func (i *ImageInstaller) Install(ctx context.Context, meta ModuleInstallMeta) (*model.ModuleRecord, error) {
	if meta.Official {
		return nil, fmt.Errorf("官方预置模块无需镜像安装，直接激活即可")
	}
	if meta.Image == nil || meta.Image.Registry == "" || meta.Image.Repository == "" {
		return nil, fmt.Errorf("第三方模块镜像信息不完整")
	}

	// 组装镜像引用：优先 digest 内容寻址，否则用 tag
	imageRef := fmt.Sprintf("%s/%s", meta.Image.Registry, meta.Image.Repository)
	if meta.Image.Digest != "" {
		imageRef += "@" + meta.Image.Digest
	} else if meta.Image.Tag != "" {
		imageRef += ":" + meta.Image.Tag
	}

	// 1. 拉镜像
	pullCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := i.pullImage(pullCtx, imageRef); err != nil {
		return nil, err
	}

	// 2. 签名校验
	verifyCtx, cancel2 := context.WithTimeout(ctx, 60*time.Second)
	defer cancel2()
	if err := i.verifySignature(verifyCtx, imageRef, meta.Signature); err != nil {
		return nil, err
	}

	// 3. 启动容器（注入模块级 auth_token 作为环境变量）
	envVars := map[string]string{}
	if meta.AuthToken != "" {
		envVars["MODULE_AUTH_TOKEN"] = meta.AuthToken
	}
	startCtx, cancel3 := context.WithTimeout(ctx, 30*time.Second)
	defer cancel3()
	url, err := i.startContainer(startCtx, meta.ModuleID, imageRef, envVars)
	if err != nil {
		return nil, err
	}

	// 4. 构造 ModuleRecord（待注册激活）
	digest := meta.Image.Digest
	if digest == "" {
		digest = meta.Image.Tag // 无 digest 时用 tag 占位（签名已校验）
	}
	return &model.ModuleRecord{
		ID:            meta.ModuleID,
		Name:          meta.Name,
		Description:   meta.Description,
		URL:           url,
		TransportType: model.ModuleTransportTypeModule,
		Status:        model.ModuleStatusOffline, // 待 registry 探测后转 online
		Version:       meta.Version,
		AuthToken:     meta.AuthToken,
		Official:      false,
		ImageRef:      imageRef,
		ImageDigest:   digest,
		Signature:     meta.Signature,
		InstallSource: "cloud-purchased",
	}, nil
}
