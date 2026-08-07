package handler_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/handler"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
	sqlite "github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// newShareHandlerSvc 构造带内存 DB 的 ModuleService + 持有 registry 引用，
// 供 SubmitForReview 测试直接注册 runtime（绕开 scanner 的 SeedOfficial 副作用）。
func newShareHandlerSvc(t *testing.T) (*service.ModuleService, *service.SkillRuntimeRegistry) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.SkillRuntime{}))
	repo := repository.NewSkillRuntimeRepo(db)
	registry := service.NewSkillRuntimeRegistry(&config.AgentReachConfig{})
	registry.SetRepo(repo)
	manager := service.NewSkillRuntimeManager(registry, zap.NewNop())
	svc := service.NewModuleService(registry, manager, repo, nil)
	return svc, registry
}

// seedShareModule 在 marketplace 根下放一个 user 脚本模块（module.json + main.py），
// 并向 registry 注册对应 SkillRuntime，使 GetModule 与 PackageModule 均可用。
func seedShareModule(t *testing.T, registry *service.SkillRuntimeRegistry, root, moduleID string) {
	t.Helper()
	modDir := filepath.Join(root, moduleID)
	require.NoError(t, os.MkdirAll(filepath.Join(modDir, "skus"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "module.json"), []byte(`{
  "id": "share-mod",
  "name": "分享模块",
  "description": "for share test",
  "source": "marketplace",
  "source_origin": "user",
  "source_actor": "alice",
  "transport": "mcp_stdio",
  "deployment": "process",
  "command": "python",
  "args": ["main.py"],
  "auto_sku": true,
  "sku_scope": "claw",
  "capabilities": ["echo"],
  "driver": {"driver_id": "share-mod", "name": "分享模块"}
}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "main.py"), []byte("print('hi')\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "skus", "echo.json"), []byte(`{"name":"echo"}`), 0o644))

	rt := &model.SkillRuntime{
		ID:           moduleID,
		Name:         "分享模块",
		Description:  "for share test",
		SourceOrigin: model.SkillRuntimeOriginUser,
		SourceActor:  "alice",
		Transport:    model.SkillRuntimeTransportMCPStdio,
		Deployment:   model.SkillRuntimeDeploymentProcess,
		Version:      "1.0.0",
	}
	rt.SetCapabilities([]string{"echo"})
	require.NoError(t, registry.Register(rt))
}

// tarEntries 解压 tar.gz 字节流，返回常规文件条目名集合。
func tarEntries(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer gz.Close()
	tr := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if hdr.Typeflag == tar.TypeReg {
			names[hdr.Name] = true
		}
	}
	return names
}

// TestSubmitForReview_ShareToCloud 验证 T8 分享到云端主流程：
// handler 查本地记录取元数据 + 打 tarball，以 multipart(metadata + tarball) 转发云端暂存端点，
// 云端验收 multipart 内容并返回 submission 记录，handler 原样中继。
func TestSubmitForReview_ShareToCloud(t *testing.T) {
	svc, registry := newShareHandlerSvc(t)
	root := t.TempDir()
	t.Setenv("CLAW_MARKETPLACE_DIR", root)
	const moduleID = "share-mod"
	seedShareModule(t, registry, root, moduleID)

	// 模拟云端 T9 暂存端点：验收 multipart(metadata JSON + tarball 文件)
	var gotMeta map[string]interface{}
	var gotTarball []byte
	cloudMux := http.NewServeMux()
	cloudMux.HandleFunc("/market/modules/submissions", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(32<<20))
		require.Len(t, r.MultipartForm.Value["metadata"], 1)
		require.NoError(t, json.Unmarshal([]byte(r.MultipartForm.Value["metadata"][0]), &gotMeta))
		f, _, err := r.FormFile("tarball")
		require.NoError(t, err)
		gotTarball, _ = io.ReadAll(f)
		_ = f.Close()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"code":    0,
			"message": "success",
			"data":    map[string]string{"submission_id": "sub-1", "status": "pending"},
		})
	})
	cloud := httptest.NewServer(cloudMux)
	defer cloud.Close()

	h := handler.NewModuleHandler(svc, zap.NewNop())
	h.SetCloudAPIBase(cloud.URL)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/claw-console/modules/submit-review", h.SubmitForReview)

	body := bytes.NewReader([]byte(`{"module_id":"share-mod"}`))
	req := httptest.NewRequest(http.MethodPost, "/claw-console/modules/submit-review", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// handler 中继云端响应
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(0), resp["code"])
	data, _ := resp["data"].(map[string]interface{})
	assert.Equal(t, "sub-1", data["submission_id"])
	assert.Equal(t, "pending", data["status"])

	// 云端收到的元数据携带 provenance（source_origin/user + source_actor/alice）
	assert.Equal(t, "share-mod", gotMeta["module_id"])
	assert.Equal(t, "分享模块", gotMeta["name"])
	assert.Equal(t, "user", gotMeta["source_origin"])
	assert.Equal(t, "alice", gotMeta["source_actor"])
	assert.Equal(t, "1.0.0", gotMeta["version"])
	assert.Equal(t, []interface{}{"echo"}, gotMeta["capabilities"])

	// 云端收到的 tarball 是合法 tar.gz，含 module.json + main.py（扁平布局）
	assert.NotEmpty(t, gotTarball)
	entries := tarEntries(t, gotTarball)
	assert.True(t, entries["module.json"])
	assert.True(t, entries["main.py"])
	assert.True(t, entries["skus/echo.json"])
}

// TestSubmitForReview_NoCloudBase 未配置云端 API Base 时返回 503。
func TestSubmitForReview_NoCloudBase(t *testing.T) {
	svc, _ := newShareHandlerSvc(t)
	h := handler.NewModuleHandler(svc, zap.NewNop())
	// 不调 SetCloudAPIBase，cloudAPIBase 为空

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/claw-console/modules/submit-review", h.SubmitForReview)

	req := httptest.NewRequest(http.MethodPost, "/claw-console/modules/submit-review",
		bytes.NewReader([]byte(`{"module_id":"share-mod"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestSubmitForReview_ModuleNotFound 本地不存在的模块返回 404（GetModule 失败先于打包）。
func TestSubmitForReview_ModuleNotFound(t *testing.T) {
	svc, _ := newShareHandlerSvc(t)
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("云端不应被调用：模块不存在应先于转发失败")
	}))
	defer cloud.Close()

	h := handler.NewModuleHandler(svc, zap.NewNop())
	h.SetCloudAPIBase(cloud.URL)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/claw-console/modules/submit-review", h.SubmitForReview)

	req := httptest.NewRequest(http.MethodPost, "/claw-console/modules/submit-review",
		bytes.NewReader([]byte(`{"module_id":"no-such-mod"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestSubmitForReview_BadRequest 缺 module_id 返回 400。
func TestSubmitForReview_BadRequest(t *testing.T) {
	svc, _ := newShareHandlerSvc(t)
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer cloud.Close()

	h := handler.NewModuleHandler(svc, zap.NewNop())
	h.SetCloudAPIBase(cloud.URL)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/claw-console/modules/submit-review", h.SubmitForReview)

	req := httptest.NewRequest(http.MethodPost, "/claw-console/modules/submit-review",
		bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
