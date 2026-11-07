package app

// sse_contract_test.go —— SSE 两 bool 从代码迁进契约后的消费侧判据（§7.34 决策）。
//
// 锁定的承重不变量：sseServerOptionsDeferred 不再硬编 auto_stream/auto_replay，
// 故契约 server.sse 段表达的值真正生效。业务 Option 追加在契约字段**之后**
// （ssecontract.Provider），硬编会覆盖契约值——把 WithAutoStream(true) /
// WithAutoReplay(true) 加回 options 即为变异，下面两条「显式 false」判据判红。
//
// 观测走 bald-admin 的实际 option 通道（sseServerOptionsDeferred，含授权钩子），
// 而非框架裸 NewServer：授权钩子要求 stream 参数 == token 主体，故桩认证器对
// "probe-token" 返回 Subject "probe"，请求一律 stream=probe&token=probe-token。

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	bootstrapv1 "github.com/kalandramo/bald/bconf/gen/go/bootstrap/v1"
	"github.com/kalandramo/bald/pkg/authn"
	baldsse "github.com/kalandramo/bald/transport/sse"
	ssecontract "github.com/kalandramo/bald/transport/sse/contract"
	"google.golang.org/protobuf/proto"
)

// sseTestAuthn 是只为授权钩子放行的桩：token "probe-token" → Subject "probe"。
type sseTestAuthn struct{}

func (sseTestAuthn) Authenticate(context.Context) (*authn.AuthClaims, error) {
	return &authn.AuthClaims{Subject: "probe"}, nil
}

func (sseTestAuthn) AuthenticateToken(token string) (*authn.AuthClaims, error) {
	if token != "probe-token" {
		return nil, errSSEUnauthorized("bad token")
	}
	return &authn.AuthClaims{Subject: "probe"}, nil
}

// startSSEViaContract 经 bald-admin 的实际 option 通道 + 契约工厂构造并启动服务器。
func startSSEViaContract(t *testing.T, sec *bootstrapv1.Server_Sse) *baldsse.Server {
	t.Helper()
	opts := sseServerOptionsDeferred(func() authn.Authenticator { return sseTestAuthn{} })
	factory := ssecontract.Provider(ssecontract.WithServerOptions(opts...))
	raw, _, err := factory(context.Background(), &bootstrapv1.Server{Sse: sec})
	if err != nil {
		t.Fatalf("contract Provider: %v", err)
	}
	srv, ok := raw.(*baldsse.Server)
	if !ok {
		t.Fatalf("server type = %T, want *sse.Server", raw)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = srv.Stop(context.Background())
	})
	go func() { _ = srv.Start(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ep := srv.Endpoint(); !strings.HasSuffix(ep, ":0") {
			return srv
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("server did not bind: Endpoint()=%q", srv.Endpoint())
	return nil
}

// TestSSEAutoStreamFromContract auto_stream 三态经契约生效（未知流首连 200/500）。
// 实现默认 false；若 options 仍硬编 true，则 unset/false 两态被盖成 true → 判红。
func TestSSEAutoStreamFromContract(t *testing.T) {
	cases := []struct {
		name   string
		value  *bool
		wantOK bool
	}{
		{"unset keeps implementation default (off)", nil, false},
		{"explicit true auto-creates", proto.Bool(true), true},
		{"explicit false stays off", proto.Bool(false), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := startSSEViaContract(t, &bootstrapv1.Server_Sse{
				Addr:       "127.0.0.1:0",
				AutoStream: tc.value,
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet,
				srv.Endpoint()+"/events?stream=probe&token=probe-token", nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET /events: %v", err)
			}
			resp.Body.Close()
			if tc.wantOK {
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status=%d, want 200 (auto_stream on)", resp.StatusCode)
				}
				if srv.GetStream("probe") == nil {
					t.Fatal("stream was not auto-created")
				}
				return
			}
			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("status=%d, want 500 (unknown stream, auto_stream off)", resp.StatusCode)
			}
			if srv.GetStream("probe") != nil {
				t.Fatal("stream must not be created when auto_stream is off")
			}
		})
	}
}

// TestSSEAutoReplayFromContract auto_replay 三态经契约生效（重连是否回放历史）。
// 实现默认 true；若 options 仍硬编 true，则「显式 false」被盖成 true → 判红。
func TestSSEAutoReplayFromContract(t *testing.T) {
	cases := []struct {
		name       string
		value      *bool
		wantReplay bool
	}{
		{"unset keeps implementation default (on)", nil, true},
		{"explicit false disables replay", proto.Bool(false), false},
		{"explicit true enables replay", proto.Bool(true), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := startSSEViaContract(t, &bootstrapv1.Server_Sse{
				Addr:       "127.0.0.1:0",
				AutoStream: proto.Bool(true),
				AutoReplay: tc.value,
			})
			srv.CreateStream("probe")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// 先挂一个订阅者：事件经事件循环处理（auto_replay 开启时同时写
			// eventLog）后才投递，读到这里即证明「已处理」。
			live := openSSEProbe(t, ctx, srv.Endpoint())
			srv.Publish(ctx, "probe", &baldsse.Event{Data: []byte("history-payload")})
			if got := readSSEData(t, live.Body, 2*time.Second); got != "history-payload" {
				t.Fatalf("live subscriber got %q, want history-payload", got)
			}
			live.Body.Close()

			// 重连者不带 Last-Event-ID（从头回放）：开启拿得到历史，关闭拿不到。
			replay := openSSEProbe(t, ctx, srv.Endpoint())
			defer replay.Body.Close()
			got, ok := trySSEData(replay.Body, 300*time.Millisecond)
			if !tc.wantReplay {
				if ok {
					t.Fatalf("replayed %q with auto_replay off", got)
				}
				return
			}
			if !ok {
				t.Fatal("no replayed data line within 300ms with auto_replay on")
			}
			if got != "history-payload" {
				t.Fatalf("replayed %q, want history-payload", got)
			}
		})
	}
}

// openSSEProbe 订阅 stream=probe（带桩 token 过授权钩子），读到响应头即返回。
func openSSEProbe(t *testing.T, ctx context.Context, endpoint string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		endpoint+"/events?stream=probe&token=probe-token", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /events?stream=probe: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	return resp
}

func readSSEData(t *testing.T, r io.Reader, timeout time.Duration) string {
	t.Helper()
	line, ok := trySSEData(r, timeout)
	if !ok {
		t.Fatalf("no SSE data line within %v", timeout)
	}
	return line
}

func trySSEData(r io.Reader, timeout time.Duration) (string, bool) {
	ch := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data:") {
				ch <- strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				return
			}
		}
	}()
	select {
	case v := <-ch:
		return v, true
	case <-time.After(timeout):
		return "", false
	}
}