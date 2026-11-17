package response_wrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/kitex/pkg/kerrors"
	apintoctx "github.com/eolinker/apinto/node/http-context"
	"github.com/eolinker/eosc/eocontext"
	"github.com/valyala/fasthttp"
)

type chainFunc func(eocontext.EoContext) error

func (f chainFunc) DoChain(ctx eocontext.EoContext) error { return f(ctx) }
func (f chainFunc) Destroy()                              {}

func TestWrap(t *testing.T) {
	biz := kerrors.NewBizStatusErrorWithExtra(401003, "用户身份不能为空", map[string]string{"internal": "private"})
	tests := []struct {
		name, body          string
		status              int
		err                 error
		conf                Config
		code, message, data string
		wantStatus          int
	}{
		{name: "JSON precision", body: `{"id":9007199254740993}`, status: 200, code: "000000", message: "成功", data: `{"id":9007199254740993}`, wantStatus: 200},
		{name: "business", body: biz.Error(), status: 400, err: fmt.Errorf("wrapped: %w", biz), code: "401003", message: "用户身份不能为空", data: "null", wantStatus: 400},
		{name: "business HTTP 200", status: 400, err: biz, conf: Config{StatusCode: 200, ErrorMessage: "fallback"}, code: "401003", message: "用户身份不能为空", data: "null", wantStatus: 200},
		{name: "timeout", status: 504, err: context.DeadlineExceeded, code: "504000", message: "下游服务调用超时", data: "null", wantStatus: 504},
		{name: "unmapped transport", status: 200, err: errors.New("internal IP and stack"), code: "502000", message: "下游服务调用失败", data: "null", wantStatus: 502},
		{name: "HTTP error", body: `{"secret":"database password"}`, status: 500, code: "500000", message: "服务调用失败", data: "null", wantStatus: 500},
		{name: "no node", status: 503, err: errors.New("no nodes"), code: "503000", message: "无可用下游服务", data: "null", wantStatus: 503},
		{name: "plugin rejection", body: "param check failed", status: 401, err: errors.New("param check failed"), code: "401000", message: "未通过身份验证", data: "null", wantStatus: 401},
		{name: "text", body: "hello", status: 200, code: "000000", message: "成功", data: `"hello"`, wantStatus: 200},
		{name: "empty", status: 200, conf: Config{SuccessMessage: "完成"}, code: "000000", message: "完成", data: "null", wantStatus: 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fasthttp.RequestCtx{}
			f.Request.Header.SetMethod("POST")
			f.Request.SetRequestURI("http://localhost/test")
			ctx := apintoctx.NewContext(f, 8099)
			defer ctx.FastFinish()
			worker, err := Create("wrap", "wrap", &tt.conf, nil)
			if err != nil {
				t.Fatal(err)
			}
			p := worker.(*ResponseWrap)
			gotErr := p.DoHttpFilter(ctx, chainFunc(func(_ eocontext.EoContext) error {
				ctx.Response().SetStatus(tt.status, "")
				ctx.Response().SetHeader("Content-Type", "text/plain")
				ctx.Response().SetHeader("ETag", "old")
				ctx.Response().SetBody([]byte(tt.body))
				return tt.err
			}))
			if !errors.Is(gotErr, tt.err) {
				t.Fatal("lost error propagation", gotErr)
			}
			var out struct {
				Code, Message string
				Data          json.RawMessage
			}
			data := ctx.Response().GetBody()
			if err := json.Unmarshal(data, &out); err != nil {
				t.Fatal(err, string(data))
			}
			if out.Code != tt.code || out.Message != tt.message || string(out.Data) != tt.data || ctx.Response().StatusCode() != tt.wantStatus {
				t.Fatalf("unexpected response %d %s", ctx.Response().StatusCode(), data)
			}
			if ctx.Response().GetHeader("ETag") != "" || !strings.HasPrefix(ctx.Response().ContentType(), "application/json") {
				t.Fatal("stale response headers")
			}
			if err := p.DoHttpFilter(ctx, nil); err != nil {
				t.Fatal(err)
			}
			if string(ctx.Response().GetBody()) != string(data) {
				t.Fatal("wrapped twice")
			}
		})
	}
}

func TestSkipNonBodyResponses(t *testing.T) {
	for _, tt := range []struct {
		method, contentType, disposition string
		status                           int
	}{{"HEAD", "application/json", "", 200}, {"GET", "application/json", "", 204}, {"GET", "application/json", "", 304}, {"GET", "text/event-stream", "", 200}, {"GET", "application/octet-stream", "attachment; filename=file", 200}} {
		f := &fasthttp.RequestCtx{}
		f.Request.Header.SetMethod(tt.method)
		f.Request.SetRequestURI("http://localhost/test")
		ctx := apintoctx.NewContext(f, 8099)
		worker, _ := Create("wrap", "wrap", &Config{StatusCode: 200}, nil)
		err := worker.(*ResponseWrap).DoHttpFilter(ctx, chainFunc(func(_ eocontext.EoContext) error {
			ctx.Response().SetStatus(tt.status, "")
			ctx.Response().SetHeader("Content-Type", tt.contentType)
			ctx.Response().SetHeader("Content-Disposition", tt.disposition)
			ctx.Response().SetBody([]byte("original"))
			return nil
		}))
		if err != nil || string(ctx.Response().GetBody()) != "original" || ctx.Response().StatusCode() != tt.status {
			t.Fatal("modified unwrappable response", tt)
		}
		ctx.FastFinish()
	}
}

func TestInvalidResetKeepsConfig(t *testing.T) {
	worker, _ := Create("wrap", "wrap", &Config{SuccessMessage: "old"}, nil)
	p := worker.(*ResponseWrap)
	if p.Reset(&Config{StatusCode: 204}, nil) == nil {
		t.Fatal("accepted invalid status")
	}
	if p.config.SuccessMessage != "old" {
		t.Fatal("invalid reset replaced config")
	}
	if err := p.Reset(&Config{StatusCode: 200}, nil); err != nil {
		t.Fatal(err)
	}
}
