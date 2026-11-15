// Package seed 承载业务种子数据（bald gen app 生成一次，此后归你维护；
// 重生成跳过本文件）。
//
// 演示域的种子起步集，对照 _example/bald 的 greet 域演示数据
// ——行形状对齐 bald.store.v1 通用 CRUD 面；保留字表演示「proto 注解表达
// 不了、需要外部状态的校验」的数据源（对照 greetv1 的 reservedNames）。
// 真实业务把 Items 换成你的域模型行，并在域 Wire / 业务 BeforeStart 里经
// 存储 Provider 落库（对照 contrib/store-gorm 范本）；无存储栈的演示域
// 保留内存语义即可。
package seed

// Item 是演示种子行（形状对齐 bald.store.v1 通用 CRUD 面：id/name/greet）。
type Item struct {
	ID    string
	Name  string
	Greet string
}

// Items 是演示域的种子行（_example/bald greet 域的等效演示数据：三条问候
// 配置——问候前缀可经域配置段 biz.greet.greeting 覆盖，见 greetv1.wire）。
var Items = []Item{
	{ID: "greet-001", Name: "bald", Greet: "hello"},
	{ID: "greet-002", Name: "world", Greet: "hi"},
	{ID: "greet-003", Name: "demo", Greet: "你好"},
}

// Reserved 是业务保留字种子（对照 greetv1 的 reservedNames：proto 注解表达
// 不了、需要外部状态的校验，其数据源从这里起步）。
var Reserved = map[string]struct{}{
	"root":   {},
	"admin":  {},
	"system": {},
}

// Seed 把种子行装载进业务存储的示范入口（真实实现：app.Resource
// ("database.sql") 取连接后落库；示例域无存储依赖，保留内存语义）。
//
// 调用点：域 Wire 或业务 BeforeStart（资源面就绪之后、server 构建之前）。
func Seed() error {
	// TODO: 读写真实存储（示例域无存储依赖——保留空实现即不污染装配）。
	return nil
}
