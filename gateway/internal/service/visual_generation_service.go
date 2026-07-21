package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/llm"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// VisualGenerationService 视觉生成业务服务
type VisualGenerationService struct {
	taskRepo              *repository.VisualTaskRepo
	convService           *VisualConversationService
	billingService        *BillingService
	eleAgentModelService  *EleAgentModelService
	settingService        *SettingService
	chatProxyService      *ChatProxyService
	uploadService         *VisualUploadService       // 用于将生成结果转存到本地，避免上游直链过期
	providers             map[string]VisualProvider // key 为 protocol，用于测试注入与兜底
	queryCache            map[string]*visualQueryCacheEntry
	queryCacheMu          sync.RWMutex
	runningImageTasks     sync.Map                  // key: taskID，防止图片任务重复执行
	logger                *zap.Logger
}

type visualQueryCacheEntry struct {
	result    *VisualQueryResult
	cachedAt  time.Time
}

// NewVisualGenerationService 创建视觉生成服务
func NewVisualGenerationService(
	taskRepo *repository.VisualTaskRepo,
	convService *VisualConversationService,
	billingService *BillingService,
	eleAgentModelService *EleAgentModelService,
	settingService *SettingService,
	chatProxyService *ChatProxyService,
	uploadService *VisualUploadService,
	logger *zap.Logger,
) *VisualGenerationService {
	if logger == nil {
		logger = zap.NewNop()
	}

	svc := &VisualGenerationService{
		taskRepo:             taskRepo,
		convService:          convService,
		billingService:       billingService,
		eleAgentModelService: eleAgentModelService,
		settingService:       settingService,
		chatProxyService:     chatProxyService,
		uploadService:        uploadService,
		providers:            make(map[string]VisualProvider),
		queryCache:           make(map[string]*visualQueryCacheEntry),
		logger:               logger,
	}

	// 注册默认 Provider；实际 API Key 在调用时从 EleAgentModelService 动态获取。
	// key 使用 protocol，与 EleAgentModelConfig.Protocol 对齐。
	svc.providers[string(model.EleAgentUpstreamAgnesImage)] = NewAgnesImageProvider("", "")
	svc.providers[string(model.EleAgentUpstreamAgnesVideo)] = NewAgnesVideoProvider("", "", "")
	svc.providers[string(model.EleAgentUpstreamSeedance)] = NewSeedanceProvider("", "")

	// 服务启动后恢复未完成的图片任务（应对重启等场景）
	go svc.recoverImageTasks()

	return svc
}

// RegisterProvider 注册/覆盖 Provider（便于测试与扩展）
// key 使用 protocol 标识，例如 agnes_image / agnes_video / seedance。
func (s *VisualGenerationService) RegisterProvider(protocol string, provider VisualProvider) {
	s.providers[protocol] = provider
}

// CreateTaskRequest 创建任务请求 DTO
type CreateTaskRequest struct {
	MediaType      string                 `json:"media_type" binding:"required"`
	Provider       string                 `json:"provider" binding:"required"`
	Model          string                 `json:"model" binding:"required"`
	Prompt         string                 `json:"prompt" binding:"required"`
	ConversationID string                 `json:"conversation_id"`
	ImageURL       string                 `json:"image_url"`
	ImageURLs      []string               `json:"image_urls"`
	Params         map[string]interface{} `json:"params"`
	Currency       string                 `json:"currency"`
}

// PromptFusionModelNotConfiguredError 连续创作所需 prompt 融合模型未配置
// 前端收到该错误后应提示用户：当前模型不支持对话记忆，请每次输入尽量完整的创作要求。
type PromptFusionModelNotConfiguredError struct {
	Message string
}

func (e *PromptFusionModelNotConfiguredError) Error() string {
	if e.Message == "" {
		return "当前模型不支持对话记忆，请每次输入尽量完整的创作要求"
	}
	return e.Message
}

// singleTurnVisualProtocols 单次任务型视觉协议，需要应用层模拟连续上下文
var singleTurnVisualProtocols = map[string]bool{
	string(model.EleAgentUpstreamAgnesImage): true,
	string(model.EleAgentUpstreamAgnesVideo): true,
	string(model.EleAgentUpstreamSeedance):   true,
	string(model.EleAgentUpstreamSeedream):   true,
}

// nativeMultimodalVisualProtocols 原生多模态对话协议，直接透传 messages 历史
var nativeMultimodalVisualProtocols = map[string]bool{
	string(model.EleAgentUpstreamOpenAIImage): true,
	string(model.EleAgentUpstreamOpenAIVideo): true,
}

// needsPromptFusion 判断指定协议是否需要 prompt 融合来模拟连续上下文
func needsPromptFusion(protocol string) bool {
	return singleTurnVisualProtocols[protocol]
}

// isNativeMultimodalVisualProtocol 判断指定协议是否为原生多模态对话协议
func isNativeMultimodalVisualProtocol(protocol string) bool {
	return nativeMultimodalVisualProtocols[protocol]
}

// CreateTask 创建视觉生成任务
// 图片任务采用异步执行：先返回 pending 状态，后台 goroutine 调用上游，前端通过轮询获取结果。
// 视频任务保持原有异步流程：同步调用上游创建任务后立即返回 pending，前端轮询查询上游状态。
func (s *VisualGenerationService) CreateTask(ctx context.Context, userID string, req *CreateTaskRequest) (*model.VisualGenerationTask, error) {
	if req.MediaType != string(model.VisualMediaTypeImage) && req.MediaType != string(model.VisualMediaTypeVideo) {
		return nil, errors.New("不支持的 media_type")
	}

	// 1. 校验模型是否已在管理后台配置
	if s.eleAgentModelService != nil && !s.eleAgentModelService.HasModel(req.Provider, req.Model) {
		return nil, fmt.Errorf("模型未配置: %s/%s", req.Provider, req.Model)
	}

	// 2. 获取模型凭证（含协议类型、API Key、BaseURL）
	cred, err := s.resolveCredential(req.Provider, req.Model)
	if err != nil {
		return nil, err
	}

	// 3. 按协议类型创建 Provider 实例
	// 原生多模态对话协议（openai_image / openai_video）当前尚未实现完整 Provider，提前给出明确提示。
	if isNativeMultimodalVisualProtocol(cred.Protocol) {
		return nil, fmt.Errorf("原生多模态视觉协议 %s 正在接入中，请暂用 agnes_image / agnes_video / seedance / seedream", cred.Protocol)
	}
	provider := s.newProviderByProtocol(cred.Protocol, cred.BaseURL, cred.APIKey)
	if provider == nil {
		return nil, fmt.Errorf("不支持的视觉协议: %s", cred.Protocol)
	}

	if provider.MediaType() != model.VisualMediaType(req.MediaType) {
		return nil, fmt.Errorf("模型 %s/%s 的协议为 %s，不支持 %s 生成，请在管理后台检查协议配置", req.Provider, req.Model, cred.Protocol, req.MediaType)
	}

	// 4. 确保任务归属到某个视觉会话
	conversationID, err := s.convService.ensureConversation(userID, req.ConversationID, req.Prompt, req.MediaType)
	if err != nil {
		return nil, err
	}

	// 5. 读取会话历史，用于 prompt 融合与参考图/视频注入
	var historyTasks []model.VisualGenerationTask
	if conversationID != "" {
		_, tasks, err := s.convService.GetConversation(conversationID, userID)
		if err != nil {
			s.logger.Warn("读取视觉会话历史失败", zap.String("conversation_id", conversationID), zap.Error(err))
		} else {
			historyTasks = tasks
		}
	}

	// 6. 单次任务型视觉协议：应用层连续上下文，进行 prompt 融合
	prompt := req.Prompt
	if needsPromptFusion(cred.Protocol) && len(historyTasks) > 0 {
		historyPrompts := extractSuccessfulTaskPrompts(historyTasks)
		if len(historyPrompts) > 0 {
			fused, err := s.fusePrompt(ctx, req.Prompt, historyPrompts)
			if err != nil {
				// 未配置融合模型时不拦截生成，降级为直接使用当前 prompt；前端会提示用户输入尽量完整。
				if _, ok := err.(*PromptFusionModelNotConfiguredError); !ok {
					return nil, err
				}
			} else {
				prompt = fused
			}
		}
	}

	// 7. 从历史任务提取参考图/视频封面，注入当前任务
	imageURL, imageURLs := s.buildInputImages(req.ImageURL, req.ImageURLs, historyTasks, model.VisualMediaType(req.MediaType))

	// 8. 视频任务限制单用户并发
	if req.MediaType == string(model.VisualMediaTypeVideo) {
		// 先清理长时间未完成的卡死任务，避免用户永远被阻塞
		s.cleanupStaleVideoTasks(userID)

		count, err := s.taskRepo.CountRunningByUser(userID)
		if err != nil {
			return nil, fmt.Errorf("查询进行中任务失败: %w", err)
		}
		if count > 0 {
			return nil, errors.New("您已有进行中的视频生成任务，请等待完成后再创建")
		}
	}

	// 9. 视频任务：校验时长参数是否在模型配置的支持范围内（未配置上限时不限制）
	if req.MediaType == string(model.VisualMediaTypeVideo) && s.eleAgentModelService != nil {
		if v, ok := req.Params["duration"].(float64); ok {
			duration := int(v)
			minDuration, maxDuration, _ := s.eleAgentModelService.GetVideoDurationLimits(req.Provider, req.Model)
			lo := minDuration
			if lo < 1 {
				lo = 1
			}
			if duration < lo || (maxDuration > 0 && duration > maxDuration) {
				if maxDuration > 0 {
					return nil, fmt.Errorf("该模型支持的视频时长为 %d-%d 秒", lo, maxDuration)
				}
				return nil, fmt.Errorf("该模型支持的视频时长最小为 %d 秒", lo)
			}
		}
	}

	// 10. 估算并校验余额
	estimatedCost := s.estimateCost(req.Provider, req.Model, req.MediaType, req.Params)
	if estimatedCost > 0 {
		if err := s.billingService.CheckBalanceGeneric(userID, req.Currency, estimatedCost); err != nil {
			return nil, &BalanceInsufficientError{Message: err.Error()}
		}
	}

	// 10. 创建 pending 任务记录
	paramsJSON, _ := json.Marshal(req.Params)
	var inputAssets []VisualInputAsset
	if imageURL != "" {
		inputAssets = append(inputAssets, VisualInputAsset{Type: "image", URL: imageURL})
	}
	for _, u := range imageURLs {
		if u != "" && u != imageURL {
			inputAssets = append(inputAssets, VisualInputAsset{Type: "image", URL: u})
		}
	}
	inputAssetsJSON, _ := json.Marshal(inputAssets)

	task := &model.VisualGenerationTask{
		ID:             generateVisualTaskID(),
		UserID:         userID,
		ConversationID: conversationID,
		MediaType:      model.VisualMediaType(req.MediaType),
		Provider:       model.VisualProvider(req.Provider),
		Model:          req.Model,
		Status:         model.VisualTaskStatusPending,
		Prompt:         prompt,
		Params:         string(paramsJSON),
		InputAssets:    string(inputAssetsJSON),
		Currency:       req.Currency,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := s.taskRepo.Create(task); err != nil {
		return nil, fmt.Errorf("保存任务失败: %w", err)
	}
	_ = s.convService.UpdateConversationTime(conversationID, userID)

	// 原生多模态对话协议：构建 messages 历史，直接透传给 Provider
	var messages []llm.Message
	if isNativeMultimodalVisualProtocol(cred.Protocol) {
		messages = buildNativeMultimodalMessages(historyTasks, prompt, imageURL, imageURLs, model.VisualMediaType(req.MediaType))
	}

	createReq := &VisualCreateRequest{
		Provider:  req.Provider,
		Model:     req.Model,
		Prompt:    prompt,
		ImageURL:  imageURL,
		ImageURLs: imageURLs,
		Messages:  messages,
		Params:    req.Params,
	}

	if req.MediaType == string(model.VisualMediaTypeImage) {
		// 11a. 图片任务：后台异步执行，立即返回 pending
		go s.runImageTaskAsync(task, cred)
		return task, nil
	}

	// 8b. 视频任务：同步调用上游创建，获取 upstreamTaskID 后更新任务状态
	createResult, err := provider.Create(ctx, createReq)
	if err != nil {
		task.Status = model.VisualTaskStatusFailed
		task.ErrorMessage = err.Error()
		task.UpdatedAt = time.Now()
		if updateErr := s.taskRepo.Update(task); updateErr != nil {
			s.logger.Warn("更新视频任务失败状态失败", zap.String("task_id", task.ID), zap.Error(updateErr))
		}
		return nil, fmt.Errorf("上游创建任务失败: %w", err)
	}

	task.UpstreamTaskID = createResult.UpstreamTaskID
	task.Status = createResult.Status
	if createResult.Result != nil {
		resultJSON, _ := json.Marshal(createResult.Result)
		task.Result = string(resultJSON)
	}
	if createResult.Usage != nil {
		usageJSON, _ := json.Marshal(createResult.Usage)
		task.Usage = string(usageJSON)
	}
	task.ErrorMessage = createResult.ErrorMessage
	task.UpdatedAt = time.Now()
	if err := s.taskRepo.Update(task); err != nil {
		return nil, fmt.Errorf("更新视频任务失败: %w", err)
	}

	return task, nil
}

// runImageTaskAsync 在后台执行图片生成任务。
// 调用方已创建 pending 任务记录，本函数负责更新状态、保存结果与扣费。
func (s *VisualGenerationService) runImageTaskAsync(task *model.VisualGenerationTask, cred *EleAgentModelCredential) {
	// 防止同一任务被重复执行（启动恢复或并发创建时）
	if _, loaded := s.runningImageTasks.LoadOrStore(task.ID, true); loaded {
		return
	}
	defer s.runningImageTasks.Delete(task.ID)

	// 任务可能已被恢复执行，先从数据库重新读取最新状态
	freshTask, err := s.taskRepo.GetByID(task.ID)
	if err != nil {
		s.logger.Warn("异步图片任务读取失败", zap.String("task_id", task.ID), zap.Error(err))
		return
	}
	if freshTask.Status != model.VisualTaskStatusPending && freshTask.Status != model.VisualTaskStatusRunning {
		// 任务已结束，无需执行
		return
	}

	// 更新为 running
	freshTask.Status = model.VisualTaskStatusRunning
	freshTask.UpdatedAt = time.Now()
	if err := s.taskRepo.Update(freshTask); err != nil {
		s.logger.Warn("更新图片任务运行状态失败", zap.String("task_id", freshTask.ID), zap.Error(err))
	}

	// 构造上游请求
	createReq, err := s.buildImageCreateRequest(freshTask)
	if err != nil {
		s.failImageTask(freshTask, fmt.Sprintf("构造请求失败: %v", err))
		return
	}

	provider := s.newProviderByProtocol(cred.Protocol, cred.BaseURL, cred.APIKey)
	if provider == nil {
		s.failImageTask(freshTask, fmt.Sprintf("不支持的视觉协议: %s", cred.Protocol))
		return
	}

	ctx := context.Background()
	var createResult *VisualCreateResult
	retryDelay := imageQueueFullInitialDelay
	upstreamAttempts := 0
	for attempt := 0; attempt <= imageQueueFullMaxRetries; attempt++ {
		var err error
		createResult, err = provider.Create(ctx, createReq)
		if err == nil {
			break
		}

		// 队列已满时由后端代为等待，任务状态保持/回退为 pending，前端显示"排队中"。
		if errors.Is(err, UpstreamQueueFullError) {
			if attempt == imageQueueFullMaxRetries {
				s.failImageTask(freshTask, "生成队列已满，请稍后重试")
				return
			}
			freshTask.Status = model.VisualTaskStatusPending
			freshTask.ErrorMessage = "正在排队等待生成资源…"
			freshTask.UpdatedAt = time.Now()
			if updateErr := s.taskRepo.Update(freshTask); updateErr != nil {
				s.logger.Warn("更新图片任务排队状态失败", zap.String("task_id", freshTask.ID), zap.Error(updateErr))
			}

			s.logger.Info("Agnes Image 队列已满，后端进入排队重试",
				zap.String("task_id", freshTask.ID),
				zap.Int("attempt", attempt+1),
				zap.Duration("delay", retryDelay))

			time.Sleep(retryDelay)

			// 等待期间用户可能取消任务，重新读取最新状态。
			freshTask, err = s.taskRepo.GetByID(task.ID)
			if err != nil {
				s.logger.Warn("排队重试前读取任务失败", zap.String("task_id", task.ID), zap.Error(err))
				return
			}
			if freshTask.Status != model.VisualTaskStatusPending && freshTask.Status != model.VisualTaskStatusRunning {
				s.logger.Info("任务在排队等待期间被结束", zap.String("task_id", task.ID), zap.String("status", string(freshTask.Status)))
				return
			}

			retryDelay *= 2
			if retryDelay > imageQueueFullMaxDelay {
				retryDelay = imageQueueFullMaxDelay
			}
			continue
		}

		// 可重试的上游错误（超时/网络异常/5xx）：聚合商不稳定时有限次重试，避免一次抖动直接判失败
		if llm.IsRetryableUpstreamErr(err) && upstreamAttempts < visualUpstreamMaxRetries {
			upstreamAttempts++
			s.logger.Warn("上游图片生成调用失败，稍后重试",
				zap.String("task_id", freshTask.ID),
				zap.Int("attempt", upstreamAttempts),
				zap.Error(err))
			freshTask.ErrorMessage = "上游服务响应异常，正在自动重试…"
			freshTask.UpdatedAt = time.Now()
			if updateErr := s.taskRepo.Update(freshTask); updateErr != nil {
				s.logger.Warn("更新图片任务重试状态失败", zap.String("task_id", freshTask.ID), zap.Error(updateErr))
			}
			time.Sleep(time.Duration(upstreamAttempts) * visualUpstreamRetryDelay)
			continue
		}

		// 其他错误直接失败，不再重试；重试耗尽的可见错误转为用户友好文案
		s.failImageTask(freshTask, friendlyVisualUpstreamError(err))
		return
	}

	// 成功：保存结果、扣费、标记完成
	freshTask.Status = createResult.Status
	if freshTask.Status == "" {
		freshTask.Status = model.VisualTaskStatusSucceeded
	}
	if createResult.Result != nil {
		// 图片任务：将上游直链转存到本地，避免链接过期后无法查看
		mirroredResult, localIDs := s.mirrorImageResult(freshTask.UserID, createResult.Result)
		resultJSON, _ := json.Marshal(mirroredResult)
		freshTask.Result = string(resultJSON)
		if len(localIDs) > 0 {
			idsJSON, _ := json.Marshal(localIDs)
			freshTask.LocalAssetIDs = string(idsJSON)
		}
	}
	if createResult.Usage != nil {
		usageJSON, _ := json.Marshal(createResult.Usage)
		freshTask.Usage = string(usageJSON)
	}
	freshTask.ErrorMessage = createResult.ErrorMessage
	freshTask.UpdatedAt = time.Now()

	if freshTask.Status == model.VisualTaskStatusSucceeded {
		s.finalizeImageTask(freshTask)
		return
	}

	if err := s.taskRepo.Update(freshTask); err != nil {
		s.logger.Warn("保存图片任务结果失败", zap.String("task_id", freshTask.ID), zap.Error(err))
	}
}

// buildImageCreateRequest 根据任务记录构造上游图片生成请求。
func (s *VisualGenerationService) buildImageCreateRequest(task *model.VisualGenerationTask) (*VisualCreateRequest, error) {
	var params map[string]interface{}
	if task.Params != "" {
		if err := json.Unmarshal([]byte(task.Params), &params); err != nil {
			return nil, fmt.Errorf("解析任务参数失败: %w", err)
		}
	}

	var imageURL string
	if task.InputAssets != "" {
		var assets []VisualInputAsset
		if err := json.Unmarshal([]byte(task.InputAssets), &assets); err != nil {
			return nil, fmt.Errorf("解析输入资源失败: %w", err)
		}
		for _, a := range assets {
			if a.Type == "image" {
				imageURL = a.URL
				break
			}
		}
	}

	return &VisualCreateRequest{
		Provider: string(task.Provider),
		Model:    task.Model,
		Prompt:   task.Prompt,
		ImageURL: imageURL,
		Params:   params,
	}, nil
}

// failImageTask 将图片任务标记为失败。
func (s *VisualGenerationService) failImageTask(task *model.VisualGenerationTask, errMsg string) {
	task.Status = model.VisualTaskStatusFailed
	task.ErrorMessage = errMsg
	task.UpdatedAt = time.Now()
	if updateErr := s.taskRepo.Update(task); updateErr != nil {
		s.logger.Warn("保存图片任务失败状态失败", zap.String("task_id", task.ID), zap.Error(updateErr))
	}
}

// failVideoTask 将视频任务标记为失败。
func (s *VisualGenerationService) failVideoTask(task *model.VisualGenerationTask, errMsg string) {
	task.Status = model.VisualTaskStatusFailed
	task.ErrorMessage = errMsg
	task.UpdatedAt = time.Now()
	now := time.Now()
	task.CompletedAt = &now
	_ = s.convService.UpdateConversationTime(task.ConversationID, task.UserID)
	if updateErr := s.taskRepo.Update(task); updateErr != nil {
		s.logger.Warn("保存视频任务失败状态失败", zap.String("task_id", task.ID), zap.Error(updateErr))
	}
}

// cleanupStaleVideoTasks 将超过最大允许时长的进行中视频任务标记为失败，
// 防止上游无回调或查询异常导致用户被永久阻塞。
const maxVideoTaskDuration = 30 * time.Minute

// 图片任务遇到上游队列满时的重试策略。
// 后端代为排队，任务状态回退到 pending，前端继续显示"排队中"。
const (
	imageQueueFullMaxRetries   = 6
	imageQueueFullInitialDelay = 2 * time.Second
	imageQueueFullMaxDelay     = 30 * time.Second
)

// 图片任务遇到可重试上游错误（超时/网络异常/5xx）时的重试策略：
// 最多额外重试 2 次（共 3 次调用），退避 3s/6s。
const (
	visualUpstreamMaxRetries  = 2
	visualUpstreamRetryDelay  = 3 * time.Second
)

func (s *VisualGenerationService) cleanupStaleVideoTasks(userID string) {
	threshold := time.Now().Add(-maxVideoTaskDuration)
	tasks, err := s.taskRepo.ListRunningVideoTasksBefore(userID, threshold)
	if err != nil {
		s.logger.Warn("查询超时视频任务失败", zap.String("user_id", userID), zap.Error(err))
		return
	}
	for i := range tasks {
		t := &tasks[i]
		s.failVideoTask(t, "任务执行超时，已自动结束")
		s.logger.Info("自动清理超时视频任务",
			zap.String("task_id", t.ID),
			zap.String("user_id", t.UserID),
			zap.Time("created_at", t.CreatedAt))
	}
}

// finalizeImageTask 对成功的图片任务进行扣费并标记完成时间。
// 用于异步 goroutine 执行成功时，或查询时发现扣费未完成的补扣场景。
func (s *VisualGenerationService) finalizeImageTask(task *model.VisualGenerationTask) {
	var usage *VisualUsage
	if task.Usage != "" {
		_ = json.Unmarshal([]byte(task.Usage), &usage)
	}

	_ = s.convService.UpdateConversationTime(task.ConversationID, task.UserID)
	cost := s.computeCost(string(task.Provider), task.Model, string(task.MediaType), usage)
	if cost > 0 {
		if err := s.billingService.DeductVisual(task.UserID, string(task.Provider), task.Model, string(task.MediaType), task.Currency, cost, 0, 0); err != nil {
			// 扣费失败记录日志，但不阻断任务完成；运营层面可后续人工处理
			s.logger.Warn("图片生成扣费失败", zap.String("task_id", task.ID), zap.Error(err))
		} else {
			task.Cost = cost
		}
	}
	now := time.Now()
	task.CompletedAt = &now
	task.UpdatedAt = now
	if err := s.taskRepo.Update(task); err != nil {
		s.logger.Warn("保存图片任务完成状态失败", zap.String("task_id", task.ID), zap.Error(err))
	}
}

// recoverImageTasks 服务启动后恢复未完成的图片任务。
func (s *VisualGenerationService) recoverImageTasks() {
	// 稍作延迟，等待其他依赖初始化完成
	time.Sleep(5 * time.Second)

	tasks, err := s.taskRepo.ListIncompleteImageTasks()
	if err != nil {
		s.logger.Warn("恢复未完成的图片任务失败", zap.Error(err))
		return
	}

	for i := range tasks {
		t := &tasks[i]
		cred, err := s.resolveCredential(string(t.Provider), t.Model)
		if err != nil {
			s.logger.Warn("恢复图片任务时获取凭证失败",
				zap.String("task_id", t.ID),
				zap.Error(err))
			s.failImageTask(t, fmt.Sprintf("恢复任务时获取凭证失败: %v", err))
			continue
		}
		s.logger.Info("恢复图片任务", zap.String("task_id", t.ID))
		go s.runImageTaskAsync(t, cred)
	}
}

// QueryTask 查询任务状态
func (s *VisualGenerationService) QueryTask(ctx context.Context, taskID, userID string) (*model.VisualGenerationTask, error) {
	task, err := s.taskRepo.GetByIDAndUser(taskID, userID)
	if err != nil {
		return nil, fmt.Errorf("任务不存在或无权访问: %w", err)
	}

	// 图片任务由后端异步执行，查询时只需读取数据库最新状态并补扣费（如异步扣费失败）
	if task.MediaType == model.VisualMediaTypeImage {
		if task.Status == model.VisualTaskStatusSucceeded && task.Cost == 0 {
			s.finalizeImageTask(task)
		}
		return task, nil
	}

	// 只有进行中的任务才刷新上游；成功/失败/取消状态直接返回
	if task.Status != model.VisualTaskStatusPending && task.Status != model.VisualTaskStatusRunning {
		return task, nil
	}

	// 5 秒查询缓存，避免高频刷新上游
	if cached := s.getQueryCache(task.UpstreamTaskID); cached != nil {
		s.applyQueryResult(task, cached)
		return task, nil
	}

	// 获取模型凭证（含协议类型）
	cred, err := s.resolveCredential(string(task.Provider), task.Model)
	if err != nil {
		return nil, err
	}

	provider := s.newProviderByProtocol(cred.Protocol, cred.BaseURL, cred.APIKey)
	if provider == nil {
		return nil, fmt.Errorf("不支持的视觉协议: %s", cred.Protocol)
	}
	queryResult, err := provider.Query(ctx, task.UpstreamTaskID)
	if err != nil {
		// 上游限流是临时状态，不要把任务标记为失败，让前端继续轮询
		if errors.Is(err, UpstreamRateLimitedError) {
			return nil, err
		}
		// 查询上游失败时，将任务标记为失败，避免用户一直卡在“进行中”
		s.failVideoTask(task, fmt.Sprintf("查询上游任务失败: %v", err))
		return task, nil
	}

	s.setQueryCache(task.UpstreamTaskID, queryResult)
	s.applyQueryResult(task, queryResult)

	// 视频任务首次完成时，将上游视频转存到本地并提取首帧封面，避免直链过期并支持连续生成。
	if task.Status == model.VisualTaskStatusSucceeded && task.Result != "" && task.LocalAssetIDs == "" {
		var result VisualResult
		if err := json.Unmarshal([]byte(task.Result), &result); err == nil {
			if result.URL != "" && !isLocalVisualFileURL(result.URL) {
				mirrored, localIDs := s.mirrorVideoResult(task.UserID, &result)
				resultJSON, _ := json.Marshal(mirrored)
				task.Result = string(resultJSON)
				if len(localIDs) > 0 {
					idsJSON, _ := json.Marshal(localIDs)
					task.LocalAssetIDs = string(idsJSON)
				}
				task.UpdatedAt = time.Now()
				if updateErr := s.taskRepo.Update(task); updateErr != nil {
					s.logger.Warn("保存视频任务本地结果失败", zap.String("task_id", task.ID), zap.Error(updateErr))
				}
			}
		}
	}

	// 任务变为终态时扣费，并刷新会话时间
	if (task.Status == model.VisualTaskStatusSucceeded || task.Status == model.VisualTaskStatusFailed) && task.Cost == 0 {
		_ = s.convService.UpdateConversationTime(task.ConversationID, userID)
		if task.Status == model.VisualTaskStatusSucceeded {
			cost := s.computeCost(string(task.Provider), task.Model, string(task.MediaType), queryResult.Usage)
			if cost > 0 {
				if err := s.billingService.DeductVisual(userID, string(task.Provider), task.Model, string(task.MediaType), task.Currency, cost, queryResult.Usage.PromptTokens, queryResult.Usage.CompletionTokens); err != nil {
					s.logger.Warn("视频生成扣费失败", zap.String("task_id", task.ID), zap.Error(err))
				} else {
					task.Cost = cost
				}
			}
		}
		now := time.Now()
		task.CompletedAt = &now
		if err := s.taskRepo.Update(task); err != nil {
			s.logger.Warn("更新任务完成时间失败", zap.String("task_id", task.ID), zap.Error(err))
		}
	}

	return task, nil
}

// CancelTask 取消任务
func (s *VisualGenerationService) CancelTask(ctx context.Context, taskID, userID string) (*model.VisualGenerationTask, error) {
	task, err := s.taskRepo.GetByIDAndUser(taskID, userID)
	if err != nil {
		return nil, fmt.Errorf("任务不存在或无权访问: %w", err)
	}

	if task.Status != model.VisualTaskStatusPending && task.Status != model.VisualTaskStatusRunning {
		return nil, errors.New("任务不在可取消状态")
	}

	cred, err := s.resolveCredential(string(task.Provider), task.Model)
	if err != nil {
		return nil, err
	}

	provider := s.newProviderByProtocol(cred.Protocol, cred.BaseURL, cred.APIKey)
	if provider == nil {
		return nil, fmt.Errorf("不支持的视觉协议: %s", cred.Protocol)
	}
	if err := provider.Cancel(ctx, task.UpstreamTaskID); err != nil {
		return nil, fmt.Errorf("取消上游任务失败: %w", err)
	}

	task.Status = model.VisualTaskStatusCancelled
	task.UpdatedAt = time.Now()
	now := time.Now()
	task.CompletedAt = &now
	_ = s.convService.UpdateConversationTime(task.ConversationID, userID)
	if err := s.taskRepo.Update(task); err != nil {
		return nil, fmt.Errorf("更新任务状态失败: %w", err)
	}

	return task, nil
}

// resolveCredential 从 EleAgentModelService 获取模型的 API Key、BaseURL 与协议类型。
func (s *VisualGenerationService) resolveCredential(provider, modelName string) (*EleAgentModelCredential, error) {
	if s.eleAgentModelService == nil {
		return nil, errors.New("EleAgentModelService 未初始化")
	}
	return s.eleAgentModelService.GetCredential(provider, modelName)
}

// mirrorImageResult 将图片生成结果中的上游 URL 转存到本地。
// 返回转存后的结果以及本地文件 ID 列表（用于会话删除时级联清理）。
// 转存失败时不阻断流程，仍保留原始 URL 并记录警告日志。
func (s *VisualGenerationService) mirrorImageResult(userID string, result *VisualResult) (*VisualResult, []string) {
	if s.uploadService == nil || result == nil {
		return result, nil
	}

	var localIDs []string
	mirror := func(rawURL string, b64 string) string {
		if rawURL == "" && b64 == "" {
			return ""
		}
		if rawURL != "" {
			uploadResult, err := s.uploadService.SaveFromURL(userID, rawURL)
			if err == nil {
				localIDs = append(localIDs, uploadResult.ID)
				return uploadResult.URL
			}
			s.logger.Warn("图片 URL 转存失败，尝试使用 Base64 兜底",
				zap.String("url", rawURL),
				zap.Error(err),
			)
		}
		// URL 下载失败或不存在时，使用 Base64 兜底
		if b64 != "" {
			uploadResult, err := s.uploadService.SaveFromBase64(userID, b64)
			if err == nil {
				localIDs = append(localIDs, uploadResult.ID)
				return uploadResult.URL
			}
			s.logger.Warn("图片 Base64 转存失败",
				zap.Error(err),
			)
		}
		// 两者都失败时保留原始 URL
		return rawURL
	}

	mirrored := &VisualResult{
		URL:      mirror(result.URL, result.B64JSON),
		CoverURL: mirror(result.CoverURL, ""),
		Width:    result.Width,
		Height:   result.Height,
		Seconds:  result.Seconds,
	}
	if len(result.URLs) > 0 {
		mirrored.URLs = make([]string, 0, len(result.URLs))
		for _, u := range result.URLs {
			mirrored.URLs = append(mirrored.URLs, mirror(u, ""))
		}
	}
	return mirrored, localIDs
}

// mirrorVideoResult 将视频生成结果中的上游视频 URL 转存到本地，并提取首帧作为封面图。
// 返回转存后的结果以及本地文件 ID 列表（用于会话删除时级联清理）。
// 转存或封面提取失败时不阻断流程，仍保留原始 URL 并记录警告日志。
func (s *VisualGenerationService) mirrorVideoResult(userID string, result *VisualResult) (*VisualResult, []string) {
	if s.uploadService == nil || result == nil {
		return result, nil
	}
	if result.URL == "" {
		return result, nil
	}

	videoResult, coverResult, err := s.uploadService.SaveVideoWithCover(userID, result.URL)
	if err != nil {
		s.logger.Warn("视频转存本地失败",
			zap.String("url", result.URL),
			zap.Error(err))
		return result, nil
	}

	var localIDs []string
	mirrored := &VisualResult{
		URL:     videoResult.URL,
		Width:   result.Width,
		Height:  result.Height,
		Seconds: result.Seconds,
		FPS:     result.FPS,
		Size:    result.Size,
	}
	localIDs = append(localIDs, videoResult.ID)

	if coverResult != nil {
		mirrored.CoverURL = coverResult.URL
		localIDs = append(localIDs, coverResult.ID)
	} else {
		// 保留上游可能已有的封面 URL（通常 Agnes Video 没有，Seedance 可能有）
		mirrored.CoverURL = result.CoverURL
	}

	return mirrored, localIDs
}

// newProviderByProtocol 根据协议类型创建带凭证的 Provider 实例
func (s *VisualGenerationService) newProviderByProtocol(protocol, baseURL, apiKey string) VisualProvider {
	if protocol == "" {
		return nil
	}

	switch protocol {
	case string(model.EleAgentUpstreamAgnesImage):
		return NewAgnesImageProvider(baseURL, apiKey)
	case string(model.EleAgentUpstreamAgnesVideo):
		return NewAgnesVideoProvider(baseURL, "", apiKey)
	case string(model.EleAgentUpstreamSeedance):
		return NewSeedanceProvider(baseURL, apiKey)
	case string(model.EleAgentUpstreamSeedream):
		return NewSeedreamProvider(baseURL, apiKey)
	default:
		return s.providers[protocol]
	}
}

// estimateCost 估算所需最小余额
// 按次附加费 price_per_generation 与 token 计费同时存在时，两者相加。
func (s *VisualGenerationService) estimateCost(provider, modelName, mediaType string, params map[string]interface{}) int64 {
	inputPrice, outputPrice, perGenPrice := s.eleAgentModelService.GetModelPricing(provider, modelName)

	if mediaType == string(model.VisualMediaTypeImage) {
		// 图片按次计费，price_per_generation 与 outputPrice 作为图片单价相加
		return perGenPrice + outputPrice
	}

	// 视频：token 费用兜底估算 + 按次附加费
	var tokenCost int64
	if outputPrice > 0 {
		tokenCost = outputPrice
	} else if inputPrice > 0 {
		tokenCost = 1
	}
	return perGenPrice + tokenCost
}

// computeCost 计算实际扣费
// 按次附加费 price_per_generation 与 token 计费同时存在时，两者相加。
func (s *VisualGenerationService) computeCost(provider, modelName, mediaType string, usage *VisualUsage) int64 {
	inputPrice, outputPrice, perGenPrice := s.eleAgentModelService.GetModelPricing(provider, modelName)

	if mediaType == string(model.VisualMediaTypeImage) {
		// 图片按次计费，price_per_generation 与 outputPrice 作为图片单价相加
		return perGenPrice + outputPrice
	}

	// 视频：实际 token 费用 + 按次附加费
	var tokenCost int64
	if usage != nil {
		inputCost := int64(usage.PromptTokens) * inputPrice / 1_000_000
		outputCost := int64(usage.CompletionTokens) * outputPrice / 1_000_000
		tokenCost = inputCost + outputCost
		// 输入或输出单价为正但 token 合计不足 1 弹丸时，最小按 1 弹丸计 token 费用
		if tokenCost == 0 && (inputPrice > 0 || outputPrice > 0) {
			tokenCost = 1
		}
	}
	return perGenPrice + tokenCost
}

// applyQueryResult 将查询结果应用到任务模型
func (s *VisualGenerationService) applyQueryResult(task *model.VisualGenerationTask, result *VisualQueryResult) {
	if result == nil {
		return
	}
	task.Status = result.Status
	if result.Result != nil {
		resultJSON, _ := json.Marshal(result.Result)
		task.Result = string(resultJSON)
	}
	if result.Usage != nil {
		usageJSON, _ := json.Marshal(result.Usage)
		task.Usage = string(usageJSON)
	}
	if result.ErrorMessage != "" {
		task.ErrorMessage = result.ErrorMessage
	}
	task.UpdatedAt = time.Now()
	if err := s.taskRepo.Update(task); err != nil {
		s.logger.Warn("更新任务状态失败", zap.String("task_id", task.ID), zap.Error(err))
	}
}

// getQueryCache 获取查询缓存
func (s *VisualGenerationService) getQueryCache(upstreamTaskID string) *VisualQueryResult {
	s.queryCacheMu.RLock()
	defer s.queryCacheMu.RUnlock()
	entry, ok := s.queryCache[upstreamTaskID]
	if !ok || time.Since(entry.cachedAt) > 5*time.Second {
		return nil
	}
	return entry.result
}

// setQueryCache 设置查询缓存
func (s *VisualGenerationService) setQueryCache(upstreamTaskID string, result *VisualQueryResult) {
	s.queryCacheMu.Lock()
	defer s.queryCacheMu.Unlock()
	s.queryCache[upstreamTaskID] = &visualQueryCacheEntry{
		result:   result,
		cachedAt: time.Now(),
	}
}

// generateVisualTaskID 生成任务 ID
func generateVisualTaskID() string {
	return "vg-" + uuid.New().String()[:8]
}

// extractSuccessfulTaskPrompts 从历史任务中提取成功任务的 prompt，按创建时间升序返回
func extractSuccessfulTaskPrompts(tasks []model.VisualGenerationTask) []string {
	var prompts []string
	for _, t := range tasks {
		if t.Status == model.VisualTaskStatusSucceeded && strings.TrimSpace(t.Prompt) != "" {
			prompts = append(prompts, t.Prompt)
		}
	}
	return prompts
}

// fusePrompt 使用配置的对话模型将用户当前指令与历史 prompt 融合为完整生成 prompt。
// 若系统未配置 prompt_fusion_model，则返回 PromptFusionModelNotConfiguredError。
func (s *VisualGenerationService) fusePrompt(ctx context.Context, currentPrompt string, historyPrompts []string) (string, error) {
	if s.settingService == nil || s.chatProxyService == nil {
		return "", &PromptFusionModelNotConfiguredError{}
	}
	settings, err := s.settingService.GetSettings()
	if err != nil {
		return "", fmt.Errorf("读取系统设置失败: %w", err)
	}
	if strings.TrimSpace(settings.PromptFusionModel) == "" {
		return "", &PromptFusionModelNotConfiguredError{}
	}

	historyText := strings.Join(historyPrompts, "\n---\n")
	userContent := fmt.Sprintf("历史创作描述：\n%s\n\n用户新的修改指令：\n%s\n\n请返回融合后的完整创作 prompt。", historyText, currentPrompt)

	req := &ChatRequest{
		Provider:  "eleagent",
		Model:     settings.PromptFusionModel,
		Messages:  []llm.Message{{Role: "system", Content: visualPromptFusionSystem}, {Role: "user", Content: userContent}},
		Stream:    false,
		MaxTokens: 2048,
	}
	chunk, err := s.chatProxyService.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("prompt 融合模型调用失败: %w", err)
	}
	if chunk == nil {
		return "", fmt.Errorf("prompt 融合模型返回为空")
	}
	fused := strings.TrimSpace(chunk.Delta)
	if fused == "" {
		return "", fmt.Errorf("prompt 融合模型未返回有效内容")
	}
	return fused, nil
}

const visualPromptFusionSystem = `你是一位视觉创作 prompt 融合专家。任务是将用户的新修改指令与历史创作描述合并为一段完整、连贯、可直接用于文生图/文生视频模型的生成 prompt。

规则：
1. 保留历史描述中的主体、风格、光线、构图、色彩、氛围等关键信息。
2. 将用户新指令作为增量修改：覆盖冲突部分，补充缺失部分，不要简单拼接。
3. 输出一段完整的中文或英文 prompt，不要解释、不要 markdown、不要多余说明。
4. 如果用户的新指令已经是一份完整 prompt，可在此基础上按历史风格微调。`

// buildInputImages 合并用户上传的参考图与历史任务结果，作为当前任务的输入资源。
// 返回值中 imageURL 为首选参考图，imageURLs 为全部去重后的参考图列表（不含首选）。
// 若历史结果 URL 是本地文件地址（/v1/visual/files/{id}），会自动转换为 Base64 Data URL，
// 以便上游视觉生成 API（如 Agnes/Seedance）能够直接消费。
func (s *VisualGenerationService) buildInputImages(userImageURL string, userImageURLs []string, historyTasks []model.VisualGenerationTask, mediaType model.VisualMediaType) (string, []string) {
	var all []string
	seen := make(map[string]bool)

	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			return
		}
		seen[u] = true
		all = append(all, u)
	}

	// 用户显式上传的参考图优先级最高；本地文件需转 Base64 后传给上游
	add(s.toReferenceDataURL(userImageURL))
	for _, u := range userImageURLs {
		add(s.toReferenceDataURL(u))
	}

	// 从历史任务提取最近一轮成功结果作为参考；本地文件转 Base64 后传给上游
	ref := extractLastSuccessfulResultURL(historyTasks, mediaType)
	if ref != "" {
		add(s.toReferenceDataURL(ref))
	} else if mediaType == model.VisualMediaTypeVideo && len(historyTasks) > 0 {
		// 视频连续生成需要封面图作为 image 参考；当前结果没有 cover_url，只能降级为 prompt 融合。
		s.logger.Warn("视频连续生成缺少封面图，不使用视觉参考，仅依赖 prompt 融合",
			zap.String("conversation_id", historyTasks[0].ConversationID))
	}

	if len(all) == 0 {
		return "", nil
	}
	if len(all) == 1 {
		return all[0], nil
	}
	return all[0], all[1:]
}

// toReferenceDataURL 将参考图 URL 转换为上游可直接消费的 Data URL。
// 本地文件（/v1/visual/files/{id}）会被读取为 Base64 Data URL；公网 URL 原样返回。
func (s *VisualGenerationService) toReferenceDataURL(rawURL string) string {
	if !isLocalVisualFileURL(rawURL) {
		return rawURL
	}
	if s.uploadService == nil {
		s.logger.Warn("参考图为本地文件但 UploadService 未初始化，将原样透传", zap.String("url", rawURL))
		return rawURL
	}
	id := extractVisualFileID(rawURL)
	if id == "" {
		return rawURL
	}
	dataURL, err := s.uploadService.GetBase64DataURL(id)
	if err != nil {
		s.logger.Warn("本地参考图转 Base64 失败，将原样透传", zap.String("url", rawURL), zap.Error(err))
		return rawURL
	}
	return dataURL
}

// isLocalVisualFileURL 判断 URL 是否为本地视觉文件地址
func isLocalVisualFileURL(rawURL string) bool {
	return strings.Contains(rawURL, "/v1/visual/files/")
}

// extractLastSuccessfulResultURL 从历史任务中提取最近一次成功结果的可复用 URL。
// 图片任务优先取 result.url；视频任务优先取 result.cover_url（封面图），无封面则取 result.url。
func extractLastSuccessfulResultURL(tasks []model.VisualGenerationTask, mediaType model.VisualMediaType) string {
	for i := len(tasks) - 1; i >= 0; i-- {
		t := tasks[i]
		if t.Status != model.VisualTaskStatusSucceeded || t.Result == "" {
			continue
		}
		var result VisualResult
		if err := json.Unmarshal([]byte(t.Result), &result); err != nil {
			continue
		}
		if url := extractResultURLForMedia(&result, mediaType); url != "" {
			return url
		}
	}
	return ""
}

// extractResultURLForMedia 从 VisualResult 中提取适合指定媒体类型的参考 URL。
// 图片任务优先取 result.url；视频任务必须取封面图 cover_url，不能把视频 URL 直接作为 image 参数传给上游。
func extractResultURLForMedia(result *VisualResult, mediaType model.VisualMediaType) string {
	if result == nil {
		return ""
	}
	if mediaType == model.VisualMediaTypeVideo {
		// 视频连续生成需要封面图（首帧）作为 image 参考；视频 URL 不是合法图片，传给上游会报 400。
		return result.CoverURL
	}
	if result.URL != "" {
		return result.URL
	}
	if len(result.URLs) > 0 && result.URLs[0] != "" {
		return result.URLs[0]
	}
	return ""
}

// buildNativeMultimodalMessages 为原生多模态对话协议构建 messages 历史。
// 每轮历史任务表示为：user(prompt) -> assistant(result image/video)。
func buildNativeMultimodalMessages(historyTasks []model.VisualGenerationTask, currentPrompt, imageURL string, imageURLs []string, mediaType model.VisualMediaType) []llm.Message {
	messages := []llm.Message{
		{Role: "system", Content: "你是一位视觉创作助手，请在对话中根据用户需求生成或修改图片/视频。"},
	}
	for _, task := range historyTasks {
		if task.Status != model.VisualTaskStatusSucceeded || task.Result == "" {
			continue
		}
		var result VisualResult
		if err := json.Unmarshal([]byte(task.Result), &result); err != nil {
			continue
		}
		refURL := extractResultURLForMedia(&result, mediaType)
		if refURL == "" {
			continue
		}
		messages = append(messages, llm.Message{Role: "user", Content: task.Prompt})
		messages = append(messages, llm.Message{
			Role: "assistant",
			Content: []interface{}{
				map[string]interface{}{
					"type":      "image_url",
					"image_url": map[string]interface{}{"url": refURL},
				},
			},
		})
	}

	currentContent := []interface{}{
		map[string]interface{}{"type": "text", "text": currentPrompt},
	}
	var allImages []string
	if imageURL != "" {
		allImages = append(allImages, imageURL)
	}
	for _, u := range imageURLs {
		if u != "" {
			allImages = append(allImages, u)
		}
	}
	for _, u := range allImages {
		currentContent = append(currentContent, map[string]interface{}{
			"type":      "image_url",
			"image_url": map[string]interface{}{"url": u},
		})
	}
	messages = append(messages, llm.Message{Role: "user", Content: currentContent})
	return messages
}
