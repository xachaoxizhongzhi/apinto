package user_permissions

import (
	"encoding/json"
	"testing"

	params "github.com/eolinker/apinto/drivers/plugins/params-check-v2"
	wrap "github.com/eolinker/apinto/drivers/plugins/response-wrap"
	apintoctx "github.com/eolinker/apinto/node/http-context"
	"github.com/eolinker/eosc/eocontext"
	httpctx "github.com/eolinker/eosc/eocontext/http-context"
	"github.com/valyala/fasthttp"
)

type chainFunc func(eocontext.EoContext) error

func (f chainFunc) DoChain(ctx eocontext.EoContext) error { return f(ctx) }
func (f chainFunc) Destroy()                              {}

func TestPermissionMembership(t *testing.T) {
	for _, tc := range []struct {
		value string
		ok    bool
	}{
		{"user:banner:list", true}, {"read, user:banner:list ,write", true}, {",,user:banner:list,,", true},
		{"", false}, {", ,", false}, {"user:banner:listAll", false}, {"prefixuser:banner:list", false},
		{"USER:BANNER:LIST", false}, {"user:banner:*", false},
	} {
		if got := contains(tc.value, "user:banner:list"); got != tc.ok {
			t.Errorf("%q: %v", tc.value, got)
		}
	}
}

func TestAuthorizationChain(t *testing.T) {
	uuid := "550e8400-e29b-41d4-a716-446655440000"
	for _, tc := range []struct {
		name, id, permissions, code string
		status                      int
		called                      bool
	}{
		{"identity missing", "", "user:banner:list", "401000", 401, false},
		{"identity invalid", "123", "user:banner:list", "401000", 401, false},
		{"permission missing", uuid, "", "403000", 403, false},
		{"substring rejected", uuid, "user:banner:listAll", "403000", 403, false},
		{"success", uuid, "user:banner:get, user:banner:list", "000000", 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fasthttp.RequestCtx{}
			f.Request.Header.SetMethod("POST")
			f.Request.SetRequestURI("http://localhost/test")
			f.Request.Header.Set("Content-Type", "application/json")
			f.Request.SetBodyString("{}")
			if tc.id != "" {
				f.Request.Header.Set("x-user-id", tc.id)
			}
			if tc.permissions != "" {
				f.Request.Header.Set("x-user-permission", tc.permissions)
			}
			ctx := apintoctx.NewContext(f, 8099)
			defer ctx.FastFinish()
			permission, err := Create("permission", "permission", &Config{Position: "header", Key: "x-user-permission", Mode: "contains", Value: "user:banner:list"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := params.Create("identity", "identity", &params.Config{Logic: "and", Params: []*params.Param{{Position: "header", Name: "x-user-id", MatchText: "~=^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			wrapper, err := wrap.Create("wrap", "wrap", &wrap.Config{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			called := false
			terminal := chainFunc(func(eocontext.EoContext) error {
				called = true
				ctx.Response().SetStatus(200, "")
				ctx.Response().SetHeader("Content-Type", "application/json")
				ctx.Response().SetBody([]byte(`{"ok":true}`))
				return nil
			})
			err = wrapper.(httpctx.HttpFilter).DoHttpFilter(ctx, chainFunc(func(eocontext.EoContext) error {
				return identity.(httpctx.HttpFilter).DoHttpFilter(ctx, chainFunc(func(eocontext.EoContext) error { return permission.(httpctx.HttpFilter).DoHttpFilter(ctx, terminal) }))
			}))
			if err != nil {
				t.Fatal(err)
			}
			var body struct{ Code string }
			if err = json.Unmarshal(ctx.Response().GetBody(), &body); err != nil {
				t.Fatal(err)
			}
			if called != tc.called || ctx.Response().StatusCode() != tc.status || body.Code != tc.code {
				t.Fatalf("called=%v status=%d response=%s", called, ctx.Response().StatusCode(), ctx.Response().GetBody())
			}
		})
	}
}

func TestHeaderOnlyDoesNotParseBody(t *testing.T) {
	f := &fasthttp.RequestCtx{}
	f.Request.SetRequestURI("http://localhost/test")
	f.Request.Header.SetMethod("POST")
	f.Request.Header.Set("Content-Type", "application/json")
	f.Request.SetBodyString("invalid JSON")
	f.Request.Header.Set("x-user-permission", "user:banner:list")
	ctx := apintoctx.NewContext(f, 8099)
	defer ctx.FastFinish()
	worker, err := Create("p", "p", &Config{Position: "header", Key: "x-user-permission", Mode: "contains", Value: "user:banner:list"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = worker.(httpctx.HttpFilter).DoHttpFilter(ctx, chainFunc(func(eocontext.EoContext) error { called = true; return nil }))
	if err != nil || !called {
		t.Fatal("header validator parsed body", err)
	}
}

func TestRejectInvalidConfig(t *testing.T) {
	for _, cfg := range []*Config{nil, {}, {Position: "body", Key: "permissions", Mode: "contains", Value: "read"}, {Position: "header", Key: "bad key", Mode: "contains", Value: "read"}, {Position: "header", Key: "x-permission", Mode: "regex", Value: "read"}, {Position: "header", Key: "x-permission", Mode: "contains", Value: "read,write"}, {Position: "header", Key: "x-permission", Mode: "contains", Value: " "}} {
		if _, err := Create("p", "p", cfg, nil); err == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
}

func TestAllPermissions(t *testing.T) {
	for _, tc := range []struct {
		name, header string
		allowed      bool
	}{
		{"all present", "write,read", true},
		{"spaces and extra items", " ,read, other, write, ", true},
		{"missing one", "read", false},
		{"repeated one", "read,read", false},
		{"substring", "read,writeAll", false},
		{"case sensitive", "read,WRITE", false},
		{"wildcard", "*", false},
		{"missing header", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fasthttp.RequestCtx{}
			f.Request.Header.SetMethod("GET")
			f.Request.SetRequestURI("http://localhost/test")
			f.Request.Header.Set("X-Custom-Permissions", tc.header)
			ctx := apintoctx.NewContext(f, 8099)
			defer ctx.FastFinish()
			worker, err := Create("p", Name, &Config{Position: "header", Key: "x-custom-permissions", Mode: "contains_all", Value: " read,write "}, nil)
			if err != nil {
				t.Fatal(err)
			}
			called := false
			err = worker.(httpctx.HttpFilter).DoHttpFilter(ctx, chainFunc(func(eocontext.EoContext) error { called = true; return nil }))
			if err != nil || called != tc.allowed {
				t.Fatalf("called=%v allowed=%v err=%v", called, tc.allowed, err)
			}
			if !tc.allowed && (ctx.Response().StatusCode() != 403 || string(ctx.Response().GetBody()) != "无权访问") {
				t.Fatal("unexpected rejection")
			}
		})
	}
}

func TestRejectInvalidAllPermissions(t *testing.T) {
	for _, value := range []string{"", " ", ",read", "read,", "read,,write", "read, ,write", "read,write\n"} {
		_, err := Create("p", Name, &Config{Position: "header", Key: "x-user-permission", Mode: "contains_all", Value: value}, nil)
		if err == nil {
			t.Fatalf("accepted invalid permissions %q", value)
		}
	}
}
