// Package taskd 是 task 域的装配声明包。
// 包定位、依赖取用约定与跨域规则见同目录 tenantd 的 domain.go 头注。
//
// 特殊点：无 proto 纯 gin 面。调度器（asynq 适配）在迟后阶段 server 构建点经装配回调 SetScheduler 注入——域经 Biz() 访问器被装配层迟到读取（见下方 wired 注释）。
package taskd

import (
	"context"
	"net/http"

	taskbiz "github.com/kalandramo/bald-admin/internal/apiserver/biz/v1/task"
	"github.com/kalandramo/bald-admin/internal/apiserver/domains"
	hgin "github.com/kalandramo/bald-admin/internal/apiserver/handler/gin"
	authmodel "github.com/kalandramo/bald-admin/internal/apiserver/model"
	"github.com/kalandramo/bald/pkg/appkit"
	"github.com/kalandramo/bald/pkg/authn"
	"github.com/kalandramo/bald/pkg/authz"
	"github.com/kalandramo/bald/pkg/store"
)

// Deps 域的外部依赖（装配层 getter 注入；语义同 tenantd.Deps——域 Wire 晚于
// 业务 BeforeStart 执行，届时仓储/认证器均已就绪）。
type Deps struct {
	// TaskStore Task 仓储（InitBridges 的 repos.Task 同一实例，无双构造）。
	TaskStore func() *store.Store[authmodel.Task]
	// Authn 认证器（带吊销检查；Routes 时点已就绪）。
	Authn func() authn.Authenticator
	// Authz 授权器（casbin 桥接；Routes 时点已就绪）。
	Authz func() authz.Authorizer
}

// wired 是域 Wire 构造的 biz 端口。装配层 asynq 回调（迟后阶段 server 构建
// 时点，晚于域 Wire——见 appkit/bootstrap.go 的域装配窗口）经 Biz() 迟到读取，
// 把 asynq server 适配成调度器注入（同一 biz 实例，无双构造）。
var wired *taskbiz.Biz

// Biz 返回 task 域的 biz 端口（未 Wire 时 nil——装配回调判空即降级不接线）。
func Biz() *taskbiz.Biz { return wired }

// Domain 返回 task 域的装配声明值（biz+routes 声明节点，通用拓扑见
// domains.StandardDomain）：routes 委托 hgin.RegisterTask——传输适配不搬家；
// 无 gRPC 面，Services 不声明。
func Domain(d Deps) appkit.Domain {
	var biz *taskbiz.Biz
	return domains.StandardDomain("task", "taskd",
		func(context.Context, *appkit.App) error {
			st, err := domains.RequireStore("taskd", "store", d.TaskStore())
			if err != nil {
				return err
			}
			biz = taskbiz.New(st)
			wired = biz
			return nil
		},
		func(h http.Handler) error {
			e, err := domains.AssertGin("taskd", h)
			if err != nil {
				return err
			}
			hgin.RegisterTask(e, d.Authn(), d.Authz(), biz)
			return nil
		},
	)
}
