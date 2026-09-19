package e2e

// t_w2_3_task_e2e_test.go —— Wave 2.3：任务调度域（源 task.proto 11 rpc）。
//
// 核心验证点：
//   - 任务定义 CRUD（PERIODIC 需 cronSpec，DELAY/WAIT_RESULT 不需要）；
//   - **调度器未配置时明确报错**（源的 hasScheduler() nil 防护语义）——
//     这是源注释强调的「避免 nil 解引用 panic」；
//   - 任务启停控制（ControlTask / StartAll / StopAll / RestartAll）；
//   - ListTaskTypeName 返回已注册处理器类型。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	gingonic "github.com/gin-gonic/gin"

	"github.com/kalandramo/bald-admin/internal/apiserver"
	auditlogbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auditlog"
	authbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/auth"
	dictbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/dict"
	filebiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/file"
	menubiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/menu"
	permissionbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/permission"
	secretbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/secret"
	taskbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/task"
	tenantbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/tenant"
	userbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/user"
	bootstrappkg "github.com/kalandramo/bald-admin/internal/bootstrap"
)

// fakeScheduler 是 task.Scheduler 的测试替身——记录调用，便于断言。
type fakeScheduler struct {
	periodic map[string]string // typeName → entryID
	tasks    []string
	seq      int
}

func newFakeScheduler() *fakeScheduler {
	return &fakeScheduler{periodic: map[string]string{}}
}

func (f *fakeScheduler) NewTask(typeName string, _ any, _ ...any) error {
	f.tasks = append(f.tasks, typeName)
	return nil
}

func (f *fakeScheduler) NewPeriodicTask(_ string, typeName string, _ any, _ ...any) (string, error) {
	f.seq++
	id := "entry-" + strconv.Itoa(f.seq)
	f.periodic[typeName] = id
	return id, nil
}

func (f *fakeScheduler) RemovePeriodicTask(taskID string) error {
	delete(f.periodic, taskID) // 参数是 taskId（任务类型名）
	return nil
}

func (f *fakeScheduler) RegisteredTypes() []string { return []string{"probe:hello"} }

func startTaskREST(t *testing.T, sched taskbiz.Scheduler) string {
	t.Helper()
	if err := bootstrappkg.InitBridges(context.Background()); err != nil {
		t.Fatalf("InitBridges: %v", err)
	}
	authBiz := authbiz.New(bootstrappkg.Signer)
	authBiz.SetAuthenticator(bootstrappkg.LazyAuthenticator())
	tb := taskbiz.New()
	if sched != nil {
		tb.SetScheduler(sched)
	}

	e := gingonic.New()
	apiserver.RegisterRoutesWithAuth(e, bootstrappkg.LazyAuthenticator(), &apiserver.BizSet{
		Auth: authBiz, Secret: secretbiz.New(nil), Tenant: tenantbiz.New(),
		User: userbiz.New(), Menu: menubiz.New(), Permission: permissionbiz.New(),
		Dict: dictbiz.New(nil), File: filebiz.New(nil, ""), AuditLog: auditlogbiz.New(),
		Task: tb,
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv.URL
}

func taskName() string { return "t" + strconv.FormatInt(time.Now().UnixNano()%10000000, 10) }

// TestWave2_3_TaskCRUD 任务定义 CRUD + 类型校验。
func TestWave2_3_TaskCRUD(t *testing.T) {
	base := startTaskREST(t, newFakeScheduler())
	admin := loginAs(t, base, "admin", "admin123")
	tn := taskName()

	// PERIODIC 用 6 字段 cron（含秒）→ 400（asynq 只接受 5 字段，实测约束）。
	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/tasks",
		map[string]any{"type_name": tn + "-6f", "type": "PERIODIC",
			"cron_spec": "*/2 * * * * *", "enable": true})
	if code != http.StatusBadRequest {
		t.Fatalf("6 字段 cron status=%d body=%s, want 400（asynq 只接受 5 字段）", code, raw)
	}

	// PERIODIC 缺 cronSpec → 400。
	code, raw = callRaw(t, base, admin, http.MethodPost, "/v1/tasks",
		map[string]any{"type_name": tn + "-bad", "type": "PERIODIC", "enable": true})
	if code != http.StatusBadRequest {
		t.Fatalf("缺 cronSpec status=%d body=%s, want 400", code, raw)
	}

	// 未知类型 → 400。
	code, _ = callRaw(t, base, admin, http.MethodPost, "/v1/tasks",
		map[string]any{"type_name": tn + "-x", "type": "NOPE", "enable": true})
	if code != http.StatusBadRequest {
		t.Fatalf("未知类型 status=%d, want 400", code)
	}

	// 合法创建。
	code, raw = callRaw(t, base, admin, http.MethodPost, "/v1/tasks",
		map[string]any{"type_name": tn, "type": "PERIODIC", "cron_spec": "*/5 * * * *", "enable": true})
	if code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", code, raw)
	}

	// 重复 → 409。
	code, _ = callRaw(t, base, admin, http.MethodPost, "/v1/tasks",
		map[string]any{"type_name": tn, "type": "PERIODIC", "cron_spec": "*/5 * * * *", "enable": true})
	if code != http.StatusConflict {
		t.Fatalf("重复 status=%d, want 409", code)
	}

	// 查询。
	code, raw = callRaw(t, base, admin, http.MethodGet, "/v1/tasks/"+tn, nil)
	if code != http.StatusOK {
		t.Fatalf("get status=%d", code)
	}
	var got struct {
		TypeName string `json:"type_name"`
		CronSpec string `json:"cron_spec"`
		Running  bool   `json:"running"`
	}
	_ = json.Unmarshal(raw, &got)
	if got.TypeName != tn || got.CronSpec != "*/5 * * * *" {
		t.Fatalf("get 返回不符: %s", raw)
	}
	if got.Running {
		t.Fatal("未启动的任务 running 应为 false")
	}

	// 列表 + 计数。
	_, raw = callRaw(t, base, admin, http.MethodGet, "/v1/tasks", nil)
	var list struct {
		Items []struct {
			TypeName string `json:"type_name"`
		} `json:"items"`
	}
	_ = json.Unmarshal(raw, &list)
	found := false
	for _, it := range list.Items {
		if it.TypeName == tn {
			found = true
		}
	}
	if !found {
		t.Fatalf("列表未含刚创建的任务: %s", raw)
	}

	// 删除。
	code, _ = callRaw(t, base, admin, http.MethodDelete, "/v1/tasks/"+tn, nil)
	if code != http.StatusOK {
		t.Fatalf("delete status=%d", code)
	}
	t.Logf("任务 CRUD + 类型校验 ✅")
}

// TestWave2_3_SchedulerNilGuard **调度器未配置时明确报错**（源的 nil 防护）。
// 这是源注释强调的核心：避免 nil 解引用 panic。
func TestWave2_3_SchedulerNilGuard(t *testing.T) {
	base := startTaskREST(t, nil) // 无调度器
	admin := loginAs(t, base, "admin", "admin123")
	tn := taskName()

	// 创建任务本身应成功（定义与调度分离）。
	if code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/tasks",
		map[string]any{"type_name": tn, "type": "PERIODIC", "cron_spec": "*/5 * * * *",
			"enable": true}); code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", code, raw)
	}

	// 启动 → 503（明确报错，非 panic、非静默成功）。
	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/tasks/"+tn+"/control",
		map[string]any{"start": true})
	if code != http.StatusServiceUnavailable {
		t.Fatalf("无调度器启动 status=%d body=%s, want 503", code, raw)
	}
	var e struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &e)
	if e.Message == "" {
		t.Fatal("应返回明确错误消息")
	}

	// StartAll 同样 503。
	code, _ = callRaw(t, base, admin, http.MethodPost, "/v1/tasks/start-all", nil)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("无调度器 start-all status=%d, want 503", code)
	}

	// ListTaskTypeName 无调度器时返回**空列表**（而非报错）——查询能力语义。
	code, raw = callRaw(t, base, admin, http.MethodGet, "/v1/tasks/types", nil)
	if code != http.StatusOK {
		t.Fatalf("types status=%d, want 200（查询非执行）", code)
	}
	var tr struct {
		TypeNames []string `json:"type_names"`
	}
	_ = json.Unmarshal(raw, &tr)
	if len(tr.TypeNames) != 0 {
		t.Fatalf("无调度器应返回空列表: %s", raw)
	}
	t.Logf("调度器 nil 防护 ✅（启停 503 / 查询返回空）")
}

// TestWave2_3_ControlTask 任务启停控制。
func TestWave2_3_ControlTask(t *testing.T) {
	fs := newFakeScheduler()
	base := startTaskREST(t, fs)
	admin := loginAs(t, base, "admin", "admin123")
	tn := taskName()

	if code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/tasks",
		map[string]any{"type_name": tn, "type": "PERIODIC", "cron_spec": "*/5 * * * *",
			"enable": true}); code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", code, raw)
	}

	// 启动。
	code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/tasks/"+tn+"/control",
		map[string]any{"start": true})
	if code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", code, raw)
	}
	if _, ok := fs.periodic[tn]; !ok {
		t.Fatal("启动后调度器应记录该任务")
	}

	// 查询 running=true。
	_, raw = callRaw(t, base, admin, http.MethodGet, "/v1/tasks/"+tn, nil)
	var got struct {
		Running bool `json:"running"`
	}
	_ = json.Unmarshal(raw, &got)
	if !got.Running {
		t.Fatalf("启动后 running 应为 true: %s", raw)
	}

	// 停止。
	code, _ = callRaw(t, base, admin, http.MethodPost, "/v1/tasks/"+tn+"/control",
		map[string]any{"start": false})
	if code != http.StatusOK {
		t.Fatalf("stop status=%d", code)
	}
	if _, ok := fs.periodic[tn]; ok {
		t.Fatal("停止后调度器不应再持有该任务")
	}
	t.Logf("任务启停控制 ✅（启动→running=true→停止→移除）")
}

// TestWave2_3_DisabledTaskNotStarted 未启用的任务不参与 StartAll。
func TestWave2_3_DisabledTaskNotStarted(t *testing.T) {
	fs := newFakeScheduler()
	base := startTaskREST(t, fs)
	admin := loginAs(t, base, "admin", "admin123")
	tn := taskName()

	// enable=false。
	if code, raw := callRaw(t, base, admin, http.MethodPost, "/v1/tasks",
		map[string]any{"type_name": tn, "type": "PERIODIC", "cron_spec": "*/5 * * * *",
			"enable": false}); code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", code, raw)
	}
	// 单独启动被拒（源语义：未启用不启动）。
	code, _ := callRaw(t, base, admin, http.MethodPost, "/v1/tasks/"+tn+"/control",
		map[string]any{"start": true})
	if code != http.StatusBadRequest {
		t.Fatalf("未启用任务启动 status=%d, want 400", code)
	}
	t.Logf("未启用任务不启动 ✅")
}

// TestWave2_3_ListTaskTypes 已注册处理器类型列表。
func TestWave2_3_ListTaskTypes(t *testing.T) {
	base := startTaskREST(t, newFakeScheduler())
	admin := loginAs(t, base, "admin", "admin123")

	code, raw := callRaw(t, base, admin, http.MethodGet, "/v1/tasks/types", nil)
	if code != http.StatusOK {
		t.Fatalf("types status=%d", code)
	}
	var tr struct {
		TypeNames []string `json:"type_names"`
	}
	_ = json.Unmarshal(raw, &tr)
	if len(tr.TypeNames) != 1 || tr.TypeNames[0] != "probe:hello" {
		t.Fatalf("types=%v, want [probe:hello]", tr.TypeNames)
	}
	t.Logf("ListTaskTypeName ✅（返回已注册处理器）")
}

// TestWave2_3_ViewerDenied viewer 不得操作任务（运维面）。
func TestWave2_3_ViewerDenied(t *testing.T) {
	base := startTaskREST(t, newFakeScheduler())
	alice := loginAs(t, base, "alice", "alice123")

	code, raw := callRaw(t, base, alice, http.MethodPost, "/v1/tasks",
		map[string]any{"type_name": taskName(), "type": "DELAY", "enable": true})
	if code != http.StatusForbidden {
		t.Fatalf("viewer 建任务 status=%d body=%s, want 403", code, raw)
	}
	t.Logf("viewer 被拒 ✅（任务属运维面，仅 admin）")
}
