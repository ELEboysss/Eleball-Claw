package service

import (
	"fmt"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
)

// AgentCredentialService 管理 SKU 凭证
type AgentCredentialService struct {
	repo      *repository.AgentCredentialRepo
	agentRepo *repository.AgentRepo
	// onModuleCredChange 模块级凭证变更钩子。env 含 ${credentials.KEY} 模板的 stdio 运行时
	// 需重 spawn 注入新 env；无模板的经 _meta per-call 注入，RespawnByDriver 内部自行跳过。
	// claw 在 main.go 注册为 manager.RespawnByDriver；cloud 不注册（stdio 非主推）。
	onModuleCredChange func(driverID, userID string) error
}

// NewAgentCredentialService 创建凭证服务
func NewAgentCredentialService(repo *repository.AgentCredentialRepo, agentRepo *repository.AgentRepo) *AgentCredentialService {
	return &AgentCredentialService{repo: repo, agentRepo: agentRepo}
}

// SetModuleCredentialChangeHook 注册模块级凭证变更钩子。
// 保存 module 桶凭证成功后异步触发，用于重启 stdio 进程以注入新 env。
func (s *AgentCredentialService) SetModuleCredentialChangeHook(fn func(driverID, userID string) error) {
	s.onModuleCredChange = fn
}

// GetManifest 返回 SKU 的 manifest，便于前端按 credentials 渲染表单
func (s *AgentCredentialService) GetManifest(agentID string) (*model.ToolManifest, error) {
	agent, err := s.agentRepo.GetByID(agentID)
	if err != nil {
		return nil, err
	}
	return agent.Manifest()
}

// bucketFor 决定凭证存储桶：scope=module 存到模块级桶 module:<driver>（同 driver 下所有 SKU 共享），
// 否则存到 SKU 级桶（agentID）。driver 取自 manifest。
func (s *AgentCredentialService) bucketFor(def model.CredentialDef, manifest *model.ToolManifest, agentID string) string {
	if def.Scope == model.CredentialScopeModule && manifest != nil && manifest.Driver != "" {
		return "module:" + string(manifest.Driver)
	}
	return agentID
}

// loadBucketValues 读取某桶的全部凭证为 map[key]value
func (s *AgentCredentialService) loadBucketValues(userID, bucket string) (map[string]string, error) {
	stored, err := s.repo.ListByUserAgent(userID, bucket)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(stored))
	for _, c := range stored {
		m[c.Key] = c.Value
	}
	return m, nil
}

// LoadModuleBucket 读取某用户在模块级桶 module:<driverID> 的凭证（stdio spawn 注入 env 用）。
func (s *AgentCredentialService) LoadModuleBucket(userID, driverID string) (map[string]string, error) {
	if userID == "" || driverID == "" {
		return nil, nil
	}
	return s.loadBucketValues(userID, "module:"+driverID)
}

// LoadModuleBucketAnyUser 读取模块级桶 module:<driverID> 下任一用户的凭证（合并）。
// 用于 stdio autostart：进程启动时尚无请求用户，claw 单用户取其已配置的模块凭证注入 env。
func (s *AgentCredentialService) LoadModuleBucketAnyUser(driverID string) (map[string]string, error) {
	if driverID == "" {
		return nil, nil
	}
	stored, err := s.repo.ListByBucket("module:" + driverID)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(stored))
	for _, c := range stored {
		m[c.Key] = c.Value
	}
	return m, nil
}

// ListForUserAgent 返回某用户某 SKU 的凭证表单（schema + 当前值）
// scope=module 的凭证从模块桶回填，因此同模块其他 SKU 配置的共享凭证也会显示已配值。
func (s *AgentCredentialService) ListForUserAgent(userID, agentID string) (map[string]interface{}, error) {
	manifest, err := s.GetManifest(agentID)
	if err != nil {
		return nil, err
	}
	if manifest == nil {
		return nil, fmt.Errorf("SKU %s 没有 manifest", agentID)
	}

	values := make(map[string]string)
	buckets := make(map[string]map[string]string) // bucket -> values（同桶只查一次）
	for key, def := range manifest.Credentials {
		bucket := s.bucketFor(def, manifest, agentID)
		bv, ok := buckets[bucket]
		if !ok {
			bv, err = s.loadBucketValues(userID, bucket)
			if err != nil {
				return nil, err
			}
			buckets[bucket] = bv
		}
		if v := bv[key]; v != "" {
			values[key] = v
		}
	}

	result := map[string]interface{}{
		"agent_id":    agentID,
		"credentials": manifest.Credentials,
		"values":      values,
	}
	return result, nil
}

// SaveForUserAgent 保存用户填入的凭证
// 只保留请求中显式传入的字段；空值且非必填的字段会被删除。
// scope=module 的凭证存到模块桶，同模块其他 SKU 立即共享。
func (s *AgentCredentialService) SaveForUserAgent(userID, agentID string, values map[string]string) error {
	manifest, err := s.GetManifest(agentID)
	if err != nil {
		return err
	}
	if manifest == nil {
		return fmt.Errorf("SKU %s 没有 manifest", agentID)
	}

	touchedModuleDriver := ""
	for key, value := range values {
		def, ok := manifest.Credentials[key]
		if !ok {
			continue
		}
		bucket := s.bucketFor(def, manifest, agentID)
		if def.Scope == model.CredentialScopeModule && manifest.Driver != "" {
			touchedModuleDriver = string(manifest.Driver)
		}
		if value == "" {
			if def.Required {
				return fmt.Errorf("%s 为必填凭证", key)
			}
			_ = s.repo.Delete(userID, bucket, key)
			continue
		}
		if err := s.repo.Save(&model.AgentUserCredential{
			UserID:  userID,
			AgentID: bucket,
			Key:     key,
			Value:   value,
		}); err != nil {
			return err
		}
	}
	// 模块级凭证变更 -> 异步通知 stdio 运行时。env 含凭证模板的需重 spawn 注入新 env；
	// 无模板的经 _meta per-call 注入，钩子内部跳过（见 RespawnByDriver）。
	if touchedModuleDriver != "" && s.onModuleCredChange != nil {
		driver, uid := touchedModuleDriver, userID
		go func() {
			_ = s.onModuleCredChange(driver, uid)
		}()
	}
	return nil
}

// LoadForExecution 返回执行时需要注入的凭证 key/value 映射
// 按 scope 从对应桶读取并合并；仅回填当前 manifest 声明的 key。
func (s *AgentCredentialService) LoadForExecution(userID, agentID string) (map[string]string, error) {
	manifest, err := s.GetManifest(agentID)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	if manifest == nil || len(manifest.Credentials) == 0 {
		return result, nil
	}
	buckets := make(map[string]map[string]string) // bucket -> values（同桶只查一次）
	for key, def := range manifest.Credentials {
		bucket := s.bucketFor(def, manifest, agentID)
		bv, ok := buckets[bucket]
		if !ok {
			bv, err = s.loadBucketValues(userID, bucket)
			if err != nil {
				return nil, err
			}
			buckets[bucket] = bv
		}
		if v := bv[key]; v != "" {
			result[key] = v
		}
	}
	return result, nil
}

// ValidateRequired 校验 SKU 声明的必填凭证是否都已提供
// 按 scope 从对应桶校验（module 凭证从模块桶查）。
func (s *AgentCredentialService) ValidateRequired(userID, agentID string, defs map[string]model.CredentialDef) error {
	manifest, err := s.GetManifest(agentID)
	if err != nil {
		return err
	}
	bucketCache := make(map[string]map[string]string)
	for key, def := range defs {
		if !def.Required {
			continue
		}
		bucket := s.bucketFor(def, manifest, agentID)
		bv, ok := bucketCache[bucket]
		if !ok {
			bv, err = s.loadBucketValues(userID, bucket)
			if err != nil {
				return err
			}
			bucketCache[bucket] = bv
		}
		if bv[key] == "" {
			return fmt.Errorf("缺少必填凭证: %s", key)
		}
	}
	return nil
}
