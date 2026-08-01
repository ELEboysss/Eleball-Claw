package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// AppConfig 全局应用配置
type AppConfig struct {
	Server      ServerConfig     `mapstructure:"server"`
	Database    DatabaseConfig   `mapstructure:"database"`
	CDKDatabase DatabaseConfig   `mapstructure:"cdk_database"`
	JWT         JWTConfig        `mapstructure:"jwt"`
	LLM         LLMConfig        `mapstructure:"llm"`
	ASR         ASRConfig        `mapstructure:"asr"`
	RateLimit   RateLimitConfig  `mapstructure:"rate_limit"`
	Release     ReleaseConfig    `mapstructure:"release"`
	Agent       AgentConfig      `mapstructure:"agent"`
	AgentReach  AgentReachConfig `mapstructure:"agent_reach"`
	Modules     ModulesConfig    `mapstructure:"modules"`
	Admin       AdminConfig      `mapstructure:"admin"`
	AdminGate   AdminGateConfig  `mapstructure:"admin_gate"`
	Payment     PaymentConfig    `mapstructure:"payment"`
	Mail        MailConfig        `mapstructure:"mail"`
}

// ModulesConfig 预置模块生命周期配置（claw 本地网关）
// auto_start：serve 启动后后台自动上线预置模块（拉镜像优先，本地构建兜底，不阻塞网关启动）；
// auto_stop：serve 收到退出信号时自动 docker compose down 由本网关启动的模块。
// docker 不可用时仅告警跳过，不影响网关本身运行。
type ModulesConfig struct {
	AutoStart bool `mapstructure:"auto_start"`
	AutoStop  bool `mapstructure:"auto_stop"`
	// Registry 镜像命名空间前缀（默认阿里云 ACR，与 CI build-modules 推送目标一致）
	Registry string `mapstructure:"registry"`
	// ImageTag 镜像标签（默认 develop，与 CI 滚动标签一致）
	ImageTag string `mapstructure:"image_tag"`
	// PullPolicy 上线策略：pull_first（拉镜像优先、构建兜底）/ build_only（仅本地构建）/ pull_only（仅拉镜像）
	PullPolicy string `mapstructure:"pull_policy"`
}

// MailConfig 邮件发送配置（用于邮箱验证码登录 OTP 发送）。
// 生产环境凭据走环境变量注入（MAIL_HOST/MAIL_USERNAME/MAIL_PASSWORD 等），
// 开发环境 enabled=false 时跳过邮件发送（OTP 可走 e2e-server stub 或控制台打印）。
type MailConfig struct {
	// Enabled 是否启用邮件发送；false 时 /auth/email/otp/send 返回「邮件未开通」
	Enabled bool `mapstructure:"enabled"`
	// Host SMTP 主机，如 smtp.qq.com
	Host string `mapstructure:"host"`
	// Port SMTP 端口，465（SSL）或 587（STARTTLS）
	Port int `mapstructure:"port"`
	// Username 发件邮箱账号
	Username string `mapstructure:"username"`
	// Password SMTP 授权码（非邮箱登录密码，QQ/Gmail 等需生成授权码）
	Password string `mapstructure:"password"`
	// From 发件人显示名，默认用 Username
	From string `mapstructure:"from"`
}

type ServerConfig struct {
	Port            int    `mapstructure:"port"`
	Mode            string `mapstructure:"mode"`
	EleagentBaseURL string `mapstructure:"eleagent_base_url"`
}

type DatabaseConfig struct {
	Driver string `mapstructure:"driver"`
	DSN    string `mapstructure:"dsn"`
}

type JWTConfig struct {
	Secret             string `mapstructure:"secret"`
	AccessExpireHours  int    `mapstructure:"access_expire_hours"`
	RefreshExpireHours int    `mapstructure:"refresh_expire_hours"`
}

type LLMConfig struct {
	Timeout    time.Duration `mapstructure:"timeout"`
	MaxRetries int           `mapstructure:"max_retries"`
}

// ASRConfig 语音识别代理配置
// 目前默认对接百度短语音识别 REST API，后续可扩展为阿里云/讯飞等多供应商。
type ASRConfig struct {
	Provider   string        `mapstructure:"provider"`
	AppID      string        `mapstructure:"app_id"`
	APIKey     string        `mapstructure:"api_key"`
	SecretKey  string        `mapstructure:"secret_key"`
	BaseURL    string        `mapstructure:"base_url"`
	Timeout    time.Duration `mapstructure:"timeout"`
	MaxAudioMB int64         `mapstructure:"max_audio_mb"`
}

type RateLimitConfig struct {
	RequestsPerMinute int     `mapstructure:"requests_per_minute"`
	ReadMultiplier    float64 `mapstructure:"read_multiplier"`
}

type ReleaseConfig struct {
	// RootPath 为 releases/ 目录所在根路径。
	// 默认使用 "releases"，即与 gateway 可执行文件工作目录相对。
	// Docker 部署时可挂载为绝对路径，如 "/app/releases"。
	RootPath string `mapstructure:"root_path"`
}

type AgentConfig struct {
	Enabled       bool   `mapstructure:"enabled"`
	BasePath      string `mapstructure:"base_path"`
	KnowledgeBase string `mapstructure:"knowledge_base"`
	Model         string `mapstructure:"model"`
	MaxSteps      int    `mapstructure:"max_steps"`
	// MaxTokensPerExecute AR-03：单次 Agent 执行的 token 预算上限（0 表示不限制）。
	// 循环内累计 usage 超限则强制进入最终回答，防止单次执行耗尽用户余额。
	MaxTokensPerExecute int `mapstructure:"max_tokens_per_execute"`
	// MaxCostPerTask AR-03：CallAssistant 子任务单次成本上限（弹丸，0 表示不限制）。
	// 编排器据此装配 env.CostGuard，每轮按 EstimateCost 估算累计成本，超限中止子任务。
	MaxCostPerTask int64 `mapstructure:"max_cost_per_task"`
	APIKey        string `mapstructure:"api_key"`
	BaseURL       string `mapstructure:"base_url"`
	// EmbeddingModel AR-09：记忆检索 embedding 模型名（EleAgent 模型中心 OpenAI 兼容 /embeddings，
	// 复用 Agent.APIKey/BaseURL 鉴权）。留空则禁用向量检索，降级 LIKE（claw 本地无 embedding 服务时留空）。
	EmbeddingModel string `mapstructure:"embedding_model"`
	// Compaction C4：对话级上下文压缩配置。
	Compaction CompactionConfig `mapstructure:"compaction"`
}

// CompactionConfig 对话级上下文压缩配置（C4）。
type CompactionConfig struct {
	// Enabled 是否启用自动压缩；手动 /agent/compact 始终可用。
	Enabled bool `mapstructure:"enabled"`
	// ThresholdTokens 自动压缩触发阈值；上下文估算 token 超过此值时触发。
	ThresholdTokens int `mapstructure:"threshold_tokens"`
	// KeepRecentTokens 保留最近消息的 token 预算（软目标）。
	KeepRecentTokens int `mapstructure:"keep_recent_tokens"`
	// FallbackModel 摘要失败时使用的降级模型；空则使用当前 Agent 模型。
	FallbackModel string `mapstructure:"fallback_model"`
}

// AgentReachConfig 集市模块客户端配置
//  formerly Agent-Reach 专属配置，现已泛化为所有 L3 独立模块的探测客户端配置。
type AgentReachConfig struct {
	// ModuleURL 默认模块服务地址（已废弃：各模块 URL 由 modules 表独立维护）
	ModuleURL string `mapstructure:"module_url"`
	// HealthCheckInterval 模块健康检查缓存间隔（请求触发时的缓存时间）
	HealthCheckInterval time.Duration `mapstructure:"health_check_interval"`
	// ProbeInterval 后台主动探测周期，默认 5 分钟；0 表示关闭后台探测
	ProbeInterval time.Duration `mapstructure:"probe_interval"`
	// Proxy 代理地址，用于受限网络（如 http://user:pass@ip:port）
	Proxy string `mapstructure:"proxy"`
}

type AdminConfig struct {
	// Enabled 是否启用 /v1/admin 管理后台接口与 admin-web。
	// 默认为 false，防止生产环境未配置即暴露管理后台；仅在显式开启后才注册相关路由。
	Enabled bool `mapstructure:"enabled"`
	// IPWhitelistEnabled 是否启用管理后台接口 IP 白名单
	IPWhitelistEnabled bool `mapstructure:"ip_whitelist_enabled"`
	// AllowedIPs 允许访问管理后台的 IP 或 CIDR 列表
	// 示例：["127.0.0.1", "10.0.0.0/8", "192.168.1.0/24"]
	AllowedIPs []string `mapstructure:"allowed_ips"`
}

// AdminGateConfig 管理后台前置闸门配置（Pre-Auth Gate）
// 在用户进入 admin-web 登录页 / 调用 /v1/admin/* 接口之前，先校验一道密钥，
// 用于隐藏登录页防扫描 + 限速防暴力破解。叠加在 JWT / IP 白名单 / 运行时开关之上。
// 详见 specs/api-schema.yml 与 docs/ai-context.md。
type AdminGateConfig struct {
	// Enabled 是否启用闸门；false 时跳过（降级回 JWT+IP 白名单，故障应急用）
	Enabled bool `mapstructure:"enabled"`
	// Token 主密钥（32 字节随机，openssl rand -hex 32 生成）
	Token string `mapstructure:"token"`
	// TokenPrev 备用密钥，轮换时先用 PREV 兜底再切主 token，实现无停机轮换
	TokenPrev string `mapstructure:"token_prev"`
	// CookieName 闸门 cookie 名，默认 eleball_admin_gate
	CookieName string `mapstructure:"cookie_name"`
	// CookieTTL cookie 有效期（秒），默认 7 天；带 sliding 续期
	CookieTTL int `mapstructure:"cookie_ttl"`
	// RatePerMinute per-IP 闸门尝试限速（次/分钟），默认 5
	RatePerMinute int `mapstructure:"rate_per_minute"`
	// FailLockCount 连续失败达此次数后锁定，默认 20
	FailLockCount int `mapstructure:"fail_lock_count"`
	// FailLockMinutes 锁定时长（分钟），默认 30
	FailLockMinutes int `mapstructure:"fail_lock_minutes"`
}

// PaymentConfig 支付渠道配置
type PaymentConfig struct {
	Alipay AlipayPaymentConfig `mapstructure:"alipay"`
	// OrderExpireMinutes 订单过期时长（分钟），超时未支付的 pending 订单由后台任务自动关闭；
	// 默认 30 分钟，与支付宝二维码 timeout_express 对齐
	OrderExpireMinutes int `mapstructure:"order_expire_minutes"`
}

// AlipayPaymentConfig 支付宝支付配置。
// 当前仅支持单次支付（当面付扫码 precreate）；签约/周期扣款（自动续费）为预留能力，
// 设计见 docs/payment-alipay-integration.md。
type AlipayPaymentConfig struct {
	// Enabled 是否启用支付宝支付；未配置真实密钥前保持 false，precreate 接口返回“支付未开通”
	Enabled bool `mapstructure:"enabled"`
	// AppID 支付宝开放平台应用 APPID
	AppID string `mapstructure:"app_id"`
	// PrivateKey 应用私钥（RSA2），严禁提交真实密钥到仓库，建议环境变量 ALIPAY_PRIVATE_KEY 注入
	PrivateKey string `mapstructure:"private_key"`
	// AlipayPublicKey 支付宝公钥（验签用，注意不是应用公钥）
	AlipayPublicKey string `mapstructure:"alipay_public_key"`
	// NotifyURL 公网可达的异步通知地址，如 https://api.eleball.cn/v1/payment/alipay/notify
	NotifyURL string `mapstructure:"notify_url"`
	// Sandbox 为 true 时使用支付宝沙箱网关（联调/测试）
	Sandbox bool `mapstructure:"sandbox"`
	// SignProductCode 预留：周期扣款（自动续费）产品码 CYCLE_PAY_AUTH_P，本期不启用
	SignProductCode string `mapstructure:"sign_product_code"`
}

// Load 从配置文件和环境变量加载配置
func Load(path string) (*AppConfig, error) {
	viper.SetConfigFile(path)
	viper.AutomaticEnv()

	// 限流默认值：写 180/min，读 = 写 * 3
	viper.SetDefault("rate_limit.requests_per_minute", 180)
	viper.SetDefault("rate_limit.read_multiplier", 3.0)
	// 支付订单默认 30 分钟过期（与支付宝二维码 timeout_express 对齐）
	viper.SetDefault("payment.order_expire_minutes", 30)
	// 邮件发送默认关闭（避免未配置即暴露 OTP 接口）
	viper.SetDefault("mail.enabled", false)
	viper.SetDefault("mail.port", 465)
	// 管理后台前置闸门默认值
	viper.SetDefault("admin_gate.enabled", false)
	viper.SetDefault("admin_gate.cookie_name", "eleball_admin_gate")
	viper.SetDefault("admin_gate.cookie_ttl", 7*24*3600) // 7 天
	viper.SetDefault("admin_gate.rate_per_minute", 5)
	viper.SetDefault("admin_gate.fail_lock_count", 20)
	viper.SetDefault("admin_gate.fail_lock_minutes", 30)

	// 预置模块生命周期：默认随 serve 自动上线/下线（无 docker 时仅告警跳过）
	viper.SetDefault("modules.auto_start", true)
	viper.SetDefault("modules.auto_stop", true)
	// 预置模块镜像：默认拉取阿里云 ACR 的 develop 滚动标签（与 CI build-modules 推送一致），
	// 拉镜像优先、本地构建兜底（pull_first）
	viper.SetDefault("modules.registry", "crpi-2tmk9w177nykk4zb.cn-hangzhou.personal.cr.aliyuncs.com/eleball")
	viper.SetDefault("modules.image_tag", "develop")
	viper.SetDefault("modules.pull_policy", "pull_first")

	// C4：对话级上下文压缩默认值。默认开启，阈值 60k，保留最近 20k，降级模型留空。
	viper.SetDefault("agent.compaction.enabled", true)
	viper.SetDefault("agent.compaction.threshold_tokens", 60000)
	viper.SetDefault("agent.compaction.keep_recent_tokens", 20000)
	viper.SetDefault("agent.compaction.fallback_model", "")

	// 允许通过环境变量覆盖关键配置，便于 Docker/脚本部署
	// jwt.secret 绑定 JWT_SECRET：claw 与云端共享 JWT 密钥，部署时注入云端 JWT_SECRET，
	// 即可让 claw-server 直验云端签发的 JWT（统一账户，无需 introspection）。
	_ = viper.BindEnv("jwt.secret", "JWT_SECRET")
	_ = viper.BindEnv("server.eleagent_base_url", "ELEAGENT_BASE_URL")
	_ = viper.BindEnv("server.port", "PORT")
	_ = viper.BindEnv("server.mode", "MODE")
	_ = viper.BindEnv("release.root_path", "RELEASE_ROOT_PATH")
	_ = viper.BindEnv("asr.provider", "ASR_PROVIDER")
	_ = viper.BindEnv("asr.app_id", "ASR_APP_ID")
	_ = viper.BindEnv("asr.api_key", "ASR_API_KEY")
	_ = viper.BindEnv("asr.secret_key", "ASR_SECRET_KEY")
	_ = viper.BindEnv("asr.base_url", "ASR_BASE_URL")
	_ = viper.BindEnv("agent.enabled", "AGENT_ENABLED")
	_ = viper.BindEnv("agent.base_path", "AGENT_BASE_PATH")
	_ = viper.BindEnv("agent.model", "AGENT_MODEL")
	_ = viper.BindEnv("agent.api_key", "AGENT_API_KEY")
	_ = viper.BindEnv("agent.base_url", "AGENT_BASE_URL")
	// C4：上下文压缩配置环境变量绑定
	_ = viper.BindEnv("agent.compaction.enabled", "AGENT_COMPACTION_ENABLED")
	_ = viper.BindEnv("agent.compaction.threshold_tokens", "AGENT_COMPACTION_THRESHOLD_TOKENS")
	_ = viper.BindEnv("agent.compaction.keep_recent_tokens", "AGENT_COMPACTION_KEEP_RECENT_TOKENS")
	_ = viper.BindEnv("agent.compaction.fallback_model", "AGENT_COMPACTION_FALLBACK_MODEL")
	_ = viper.BindEnv("agent_reach.module_url", "AGENT_REACH_MODULE_URL")
	_ = viper.BindEnv("agent_reach.health_check_interval", "AGENT_REACH_HEALTH_CHECK_INTERVAL")
	_ = viper.BindEnv("agent_reach.probe_interval", "AGENT_REACH_PROBE_INTERVAL")
	_ = viper.BindEnv("agent_reach.proxy", "AGENT_REACH_PROXY")
	_ = viper.BindEnv("modules.auto_start", "MODULES_AUTO_START")
	_ = viper.BindEnv("modules.auto_stop", "MODULES_AUTO_STOP")
	_ = viper.BindEnv("modules.registry", "MODULES_REGISTRY")
	_ = viper.BindEnv("modules.image_tag", "MODULES_IMAGE_TAG")
	_ = viper.BindEnv("modules.pull_policy", "MODULES_PULL_POLICY")
	_ = viper.BindEnv("admin.enabled", "ADMIN_ENABLED")
	_ = viper.BindEnv("admin.ip_whitelist_enabled", "ADMIN_IP_WHITELIST_ENABLED")
	_ = viper.BindEnv("admin.allowed_ips", "ADMIN_ALLOWED_IPS")
	// 管理后台前置闸门（Pre-Auth Gate）
	_ = viper.BindEnv("admin_gate.enabled", "ADMIN_GATE_ENABLED")
	_ = viper.BindEnv("admin_gate.token", "ADMIN_GATE_TOKEN")
	_ = viper.BindEnv("admin_gate.token_prev", "ADMIN_GATE_TOKEN_PREV")
	_ = viper.BindEnv("admin_gate.cookie_name", "ADMIN_GATE_COOKIE_NAME")
	_ = viper.BindEnv("admin_gate.cookie_ttl", "ADMIN_GATE_COOKIE_TTL")
	_ = viper.BindEnv("admin_gate.rate_per_minute", "ADMIN_GATE_RATE_PER_MINUTE")
	_ = viper.BindEnv("admin_gate.fail_lock_count", "ADMIN_GATE_FAIL_LOCK_COUNT")
	_ = viper.BindEnv("admin_gate.fail_lock_minutes", "ADMIN_GATE_FAIL_LOCK_MINUTES")
	_ = viper.BindEnv("payment.alipay.enabled", "ALIPAY_ENABLED")
	_ = viper.BindEnv("payment.alipay.app_id", "ALIPAY_APP_ID")
	_ = viper.BindEnv("payment.alipay.private_key", "ALIPAY_PRIVATE_KEY")
	_ = viper.BindEnv("payment.alipay.alipay_public_key", "ALIPAY_PUBLIC_KEY")
	_ = viper.BindEnv("payment.alipay.notify_url", "ALIPAY_NOTIFY_URL")
	_ = viper.BindEnv("payment.alipay.sandbox", "ALIPAY_SANDBOX")
	_ = viper.BindEnv("payment.alipay.sign_product_code", "ALIPAY_SIGN_PRODUCT_CODE")
	_ = viper.BindEnv("payment.order_expire_minutes", "PAYMENT_ORDER_EXPIRE_MINUTES")
	// 邮件发送配置（邮箱 OTP 登录用）
	_ = viper.BindEnv("mail.enabled", "MAIL_ENABLED")
	_ = viper.BindEnv("mail.host", "MAIL_HOST")
	_ = viper.BindEnv("mail.port", "MAIL_PORT")
	_ = viper.BindEnv("mail.username", "MAIL_USERNAME")
	_ = viper.BindEnv("mail.password", "MAIL_PASSWORD")
	_ = viper.BindEnv("mail.from", "MAIL_FROM")

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg AppConfig
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}

	// 管理后台闸门强校验：启用闸门时必须配置至少一个有效密钥（主或备），
	// 否则拒绝启动，对齐 ENCRYPTION_MASTER_KEY 的强校验风格。
	if cfg.AdminGate.Enabled {
		if cfg.AdminGate.Token == "" && cfg.AdminGate.TokenPrev == "" {
			return nil, fmt.Errorf("admin_gate.enabled=true 但未配置 ADMIN_GATE_TOKEN / ADMIN_GATE_TOKEN_PREV，拒绝启动")
		}
		if cfg.AdminGate.CookieTTL <= 0 {
			cfg.AdminGate.CookieTTL = 7 * 24 * 3600
		}
		if cfg.AdminGate.RatePerMinute <= 0 {
			cfg.AdminGate.RatePerMinute = 5
		}
		if cfg.AdminGate.FailLockCount <= 0 {
			cfg.AdminGate.FailLockCount = 20
		}
		if cfg.AdminGate.FailLockMinutes <= 0 {
			cfg.AdminGate.FailLockMinutes = 30
		}
	}

	// 邮件配置强校验：启用邮件但缺关键配置时拒绝启动，对齐其他敏感配置风格。
	if cfg.Mail.Enabled {
		if cfg.Mail.Host == "" || cfg.Mail.Username == "" || cfg.Mail.Password == "" {
			return nil, fmt.Errorf("mail.enabled=true 但未配置 MAIL_HOST/MAIL_USERNAME/MAIL_PASSWORD，拒绝启动")
		}
		if cfg.Mail.Port <= 0 {
			cfg.Mail.Port = 465
		}
		if cfg.Mail.From == "" {
			cfg.Mail.From = cfg.Mail.Username
		}
	}

	return &cfg, nil
}
