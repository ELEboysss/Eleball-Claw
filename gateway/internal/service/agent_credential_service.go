package service

import (
	"fmt"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
)

// AgentCredentialService 管理 SKU 凭证
type AgentCredentialService struct {
	repo     *repository.AgentCredentialRepo
	agentRepo *repository.AgentRepo
}

// NewAgentCredentialService 创建凭证服务
func NewAgentCredentialService(repo *repository.AgentCredentialRepo, agentRepo *repository.AgentRepo) *AgentCredentialService {
	return &AgentCredentialService{repo: repo, agentRepo: agentRepo}
}

// GetManifest 返回 SKU 的 manifest，便于前端按 credentials 渲染表单
func (s *AgentCredentialService) GetManifest(agentID string) (*model.ToolManifest, error) {
	agent, err := s.agentRepo.GetByID(agentID)
	if err != nil {
		return nil, err
	}
	return agent.Manifest()
}

// ListForUserAgent 返回某用户某 SKU 的凭证表单（schema + 当前值）
func (s *AgentCredentialService) ListForUserAgent(userID, agentID string) (map[string]interface{}, error) {
	manifest, err := s.GetManifest(agentID)
	if err != nil {
		return nil, err
	}
	if manifest == nil {
		return nil, fmt.Errorf("SKU %s 没有 manifest", agentID)
	}

	stored, err := s.repo.ListByUserAgent(userID, agentID)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string)
	for _, c := range stored {
		values[c.Key] = c.Value
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
func (s *AgentCredentialService) SaveForUserAgent(userID, agentID string, values map[string]string) error {
	manifest, err := s.GetManifest(agentID)
	if err != nil {
		return err
	}
	if manifest == nil {
		return fmt.Errorf("SKU %s 没有 manifest", agentID)
	}

	for key, value := range values {
		def, ok := manifest.Credentials[key]
		if !ok {
			continue
		}
		if value == "" {
			if def.Required {
				return fmt.Errorf("%s 为必填凭证", key)
			}
			_ = s.repo.Delete(userID, agentID, key)
			continue
		}
		if err := s.repo.Save(&model.AgentUserCredential{
			UserID:  userID,
			AgentID: agentID,
			Key:     key,
			Value:   value,
		}); err != nil {
			return err
		}
	}
	return nil
}

// LoadForExecution 返回执行时需要注入的凭证 key/value 映射
func (s *AgentCredentialService) LoadForExecution(userID, agentID string) (map[string]string, error) {
	creds, err := s.repo.ListByUserAgent(userID, agentID)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(creds))
	for _, c := range creds {
		result[c.Key] = c.Value
	}
	return result, nil
}

// ValidateRequired 校验 SKU 声明的必填凭证是否都已提供
func (s *AgentCredentialService) ValidateRequired(userID, agentID string, defs map[string]model.CredentialDef) error {
	stored, err := s.repo.ListByUserAgent(userID, agentID)
	if err != nil {
		return err
	}
	values := make(map[string]string)
	for _, c := range stored {
		values[c.Key] = c.Value
	}
	for key, def := range defs {
		if !def.Required {
			continue
		}
		if values[key] == "" {
			return fmt.Errorf("缺少必填凭证: %s", key)
		}
	}
	return nil
}
