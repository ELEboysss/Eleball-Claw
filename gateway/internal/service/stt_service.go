package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// SttService 语音识别代理服务
// 目前对接百度短语音识别 REST API；客户端只需上传音频文件，由网关完成鉴权、token 刷新与供应商调用。
type SttService struct {
	provider   string
	appID      string
	apiKey     string
	secretKey  string
	baseURL    string
	timeout    time.Duration
	maxAudioMB int64
	logger     *zap.Logger

	mu        sync.RWMutex
	accessToken    string
	tokenExpireAt  time.Time
	httpClient     *http.Client
}

// SttResult 语音识别结果

type SttResult struct {
	Text     string `json:"text"`
	Provider string `json:"provider"`
}

// NewSttService 创建语音识别代理服务
func NewSttService(provider, appID, apiKey, secretKey, baseURL string, timeout time.Duration, maxAudioMB int64, logger *zap.Logger) *SttService {
	if provider == "" {
		provider = "baidu"
	}
	if baseURL == "" {
		baseURL = "https://vop.baidu.com"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if maxAudioMB <= 0 {
		maxAudioMB = 10
	}
	return &SttService{
		provider:   provider,
		appID:      appID,
		apiKey:     apiKey,
		secretKey:  secretKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		timeout:    timeout,
		maxAudioMB: maxAudioMB,
		logger:     logger,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// IsEnabled 服务是否可用（已配置必要凭证）
func (s *SttService) IsEnabled() bool {
	if s == nil {
		return false
	}
	if s.provider != "baidu" {
		return false
	}
	return s.appID != "" && s.apiKey != "" && s.secretKey != ""
}

// Transcribe 将音频文件转录为文本
// language 目前仅作保留字段，百度中文识别默认使用 zh。
func (s *SttService) Transcribe(audio io.Reader, filename string, size int64, language string) (*SttResult, error) {
	if !s.IsEnabled() {
		return nil, fmt.Errorf("语音识别服务未配置")
	}

	if size <= 0 {
		return nil, fmt.Errorf("音频文件为空")
	}

	maxBytes := s.maxAudioMB * 1024 * 1024
	if size > maxBytes {
		return nil, fmt.Errorf("音频文件超过 %d MB 限制", s.maxAudioMB)
	}

	// 读取音频内容
	audioBytes, err := io.ReadAll(io.LimitReader(audio, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取音频失败: %w", err)
	}
	if int64(len(audioBytes)) > maxBytes {
		return nil, fmt.Errorf("音频文件超过 %d MB 限制", s.maxAudioMB)
	}

	// 获取百度 access_token
	token, err := s.getAccessToken()
	if err != nil {
		return nil, fmt.Errorf("获取语音识别凭证失败: %w", err)
	}

	// 调用百度语音识别接口
	text, err := s.baiduRecognize(audioBytes, token)
	if err != nil {
		return nil, err
	}

	return &SttResult{
		Text:     text,
		Provider: s.provider,
	}, nil
}

// baiduTokenResponse 百度 access_token 响应

type baiduTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

// getAccessToken 获取并缓存百度 access_token
func (s *SttService) getAccessToken() (string, error) {
	s.mu.RLock()
	token := s.accessToken
	expireAt := s.tokenExpireAt
	s.mu.RUnlock()

	if token != "" && time.Now().Add(5*time.Minute).Before(expireAt) {
		return token, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 双重检查
	if s.accessToken != "" && time.Now().Add(5*time.Minute).Before(s.tokenExpireAt) {
		return s.accessToken, nil
	}

	reqURL := fmt.Sprintf(
		"https://aip.baidubce.com/oauth/2.0/token?grant_type=client_credentials&client_id=%s&client_secret=%s",
		url.QueryEscape(s.apiKey),
		url.QueryEscape(s.secretKey),
	)

	req, err := http.NewRequest(http.MethodPost, reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var tr baiduTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("解析 token 响应失败: %w", err)
	}
	if tr.Error != "" {
		return "", fmt.Errorf("百度 token 错误: %s (%s)", tr.Error, tr.Description)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("百度 token 为空")
	}

	s.accessToken = tr.AccessToken
	expiresIn := tr.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 2592000 // 百度默认 30 天
	}
	s.tokenExpireAt = time.Now().Add(time.Duration(expiresIn) * time.Second)

	return tr.AccessToken, nil
}

// baiduRecognizeRequest 百度语音识别请求体

type baiduRecognizeRequest struct {
	Format  string `json:"format"`
	Rate    int    `json:"rate"`
	Channel int    `json:"channel"`
	CUID    string `json:"cuid"`
	Token   string `json:"token"`
	Speech  string `json:"speech"`
	Len     int    `json:"len"`
	DevPid  int    `json:"dev_pid"`
}

// baiduRecognizeResponse 百度语音识别响应体

type baiduRecognizeResponse struct {
	ErrNo   int      `json:"err_no"`
	ErrMsg  string   `json:"err_msg"`
	Result  []string `json:"result"`
	Sn      string   `json:"sn"`
}

// baiduRecognize 调用百度语音识别
func (s *SttService) baiduRecognize(audio []byte, token string) (string, error) {
	format := s.audioFormat(audio)
	rate := 16000
	if format == "amr" {
		rate = 8000
	}

	reqBody := baiduRecognizeRequest{
		Format:  format,
		Rate:    rate,
		Channel: 1,
		CUID:    s.appID,
		Token:   token,
		Speech:  base64.StdEncoding.EncodeToString(audio),
		Len:     len(audio),
		DevPid:  1537, // 普通话(纯中文识别)
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	reqURL := fmt.Sprintf("%s/server_api", s.baseURL)
	req, err := http.NewRequest(http.MethodPost, reqURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("调用百度语音识别失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var br baiduRecognizeResponse
	if err := json.Unmarshal(body, &br); err != nil {
		return "", fmt.Errorf("解析语音识别响应失败: %w", err)
	}

	if br.ErrNo != 0 {
		return "", fmt.Errorf("百度语音识别错误: %s (code=%d)", br.ErrMsg, br.ErrNo)
	}

	if len(br.Result) == 0 || br.Result[0] == "" {
		return "", fmt.Errorf("未能识别到语音")
	}

	return strings.TrimSpace(br.Result[0]), nil
}

// audioFormat 根据文件魔数判断音频格式
func (s *SttService) audioFormat(audio []byte) string {
	if len(audio) < 12 {
		return "pcm"
	}
	// m4a/mp4 ftyp box
	if bytes.Equal(audio[4:8], []byte("ftyp")) {
		return "m4a"
	}
	// WAV RIFF
	if bytes.Equal(audio[0:4], []byte("RIFF")) && bytes.Equal(audio[8:12], []byte("WAVE")) {
		return "wav"
	}
	// AMR
	if bytes.Equal(audio[0:5], []byte("#!AMR")) {
		return "amr"
	}
	return "pcm"
}

// ExtractFileSizeFromMultipart 从 multipart 表单中估算文件大小（用于未提供 size 时的兜底校验）
func ExtractFileSizeFromMultipart(file multipart.File) int64 {
	// 简单实现：尝试 Seek 到末尾获取大小
	if seeker, ok := file.(io.Seeker); ok {
		if pos, err := seeker.Seek(0, io.SeekEnd); err == nil {
			_, _ = seeker.Seek(0, io.SeekStart)
			return pos
		}
	}
	return -1
}
