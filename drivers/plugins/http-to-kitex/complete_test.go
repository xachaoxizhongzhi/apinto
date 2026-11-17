package http_to_kitex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	thrift "github.com/eolinker/apinto/drivers/transcode/thrift"
	"github.com/eolinker/eosc"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/gopkg/cloud/metainfo"
	"github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/client/genericclient"
	"github.com/cloudwego/kitex/pkg/generic"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/transmeta"
	"github.com/cloudwego/kitex/server"
	"github.com/cloudwego/kitex/server/genericserver"
	"github.com/cloudwego/kitex/transport"
	response_wrap "github.com/eolinker/apinto/drivers/plugins/response-wrap"
	"github.com/eolinker/apinto/entries/ctx_key"
	apintoctx "github.com/eolinker/apinto/node/http-context"
	"github.com/eolinker/eosc/eocontext"
	"github.com/valyala/fasthttp"
)

func sampleFiles() map[string]string {
	return map[string]string{
		"idl/types.thrift": `struct Request {
  1: optional i64 id (api.path="id")
  2: optional string message (api.body="message")
  3: optional string q (api.query="q")
  4: optional string trace (api.header="X-Trace")
}
struct PlainRequest {
  1: i64 id
  2: string message
  3: list<string> tags
}
struct Response {
  1: i64 id (api.js_conv="")
  2: string message
  3: string q
  4: string trace
  5: map<string,string> metadata
  6: i64 raw_id
  7: string rpc_method
  8: list<string> tags
}`,
		"idl/api.thrift": `include "types.thrift"
service Demo {
  types.Response Plain(1: types.PlainRequest req)
  types.Response PlainOther(1: types.PlainRequest req)
  types.Response Get(1: types.Request req)(api.get="/items/:id")
  types.Response Put(1: types.Request req)(api.post="/items")
  types.Response Other(1: types.Request req)(api.get="/other")
  types.Response Bad(1: types.Request req)(api.get="/bad")
  types.Response Slow(1: types.Request req)(api.get="/slow")
}`}
}

type contractTestWorkers map[string]eosc.IWorker

func (w contractTestWorkers) Get(id string) (eosc.IWorker, bool) { v, ok := w[id]; return v, ok }

var testContracts = contractTestWorkers{}

func testContract(conf *Config, files map[string]string) *Config {
	raw, _ := json.Marshal(files)
	sum := sha256.Sum256(raw)
	rev := hex.EncodeToString(sum[:])
	name := "thrift-" + rev
	id := name + "@transcode"
	if _, ok := testContracts[id]; !ok {
		w, err := thrift.Create(id, name, &thrift.Config{Revision: rev, Files: files}, nil)
		if err != nil {
			panic(err)
		}
		testContracts[id] = w
	}
	resourceOnce.Do(func() {})
	resourceWorkers = testContracts
	conf.Contract = eosc.RequireId(id)
	return conf
}
func sampleConfig(method string) *Config {
	return testContract(&Config{Service: "kitex-example", MainIDL: "idl/api.thrift", Method: method}, sampleFiles())
}

type testNode struct {
	eocontext.INode
	address string
}

func (n testNode) Addr() string { return n.address }

type testBalance struct {
	eocontext.BalanceHandler
	address string
	timeout time.Duration
}

func (b testBalance) TimeOut() time.Duration { return b.timeout }

func (b testBalance) Select(eocontext.EoContext) (eocontext.INode, int, error) {
	if b.address == "" {
		return nil, 0, fmt.Errorf("no nodes")
	}
	return testNode{address: b.address}, 0, nil
}

type chainFunc func(eocontext.EoContext) error

func (f chainFunc) DoChain(c eocontext.EoContext) error { return f(c) }
func (f chainFunc) Destroy()                            {}

func TestHTTPToKitex(t *testing.T) {
	t.Run("dynamicgo", func(t *testing.T) { testHTTPToKitex(t, true) })
	t.Run("standard_fallback", func(t *testing.T) { testHTTPToKitex(t, false) })
}

func testHTTPToKitex(t *testing.T, dynamic bool) {
	conf := sampleConfig("Get")
	provider, err := generic.NewThriftContentWithAbsIncludePathProvider(conf.MainIDL, sampleFiles())
	if err != nil {
		t.Fatal(err)
	}
	codec, err := generic.MapThriftGeneric(provider)
	if err != nil {
		t.Fatal(err)
	}
	defer codec.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	svr := genericserver.NewServerV2(&generic.ServiceV2{GenericCall: func(ctx context.Context, service, method string, request interface{}) (interface{}, error) {
		calls.Add(1)
		if service != "Demo" {
			return nil, fmt.Errorf("wrong IDL service %s", service)
		}
		if method == "Bad" {
			return nil, kerrors.NewBizStatusError(400001, "invalid argument")
		}
		if method == "Slow" {
			time.Sleep(100 * time.Millisecond)
		}
		req := request.(map[string]interface{})
		id, _ := req["id"].(int64)
		message, _ := req["message"].(string)
		q, _ := req["q"].(string)
		trace, _ := req["trace"].(string)
		metadata := map[interface{}]interface{}{}
		for _, key := range []string{"x-user-id", "client-device", "unmapped"} {
			if value, ok := metainfo.GetPersistentValue(ctx, key); ok {
				metadata[key] = value
			}
		}
		return map[string]interface{}{"id": id, "message": message, "q": q, "trace": trace, "metadata": metadata, "raw_id": int64(9007199254740993), "rpc_method": method, "tags": req["tags"]}, nil
	}}, codec, server.WithListener(ln), server.WithMetaHandler(transmeta.ServerTTHeaderHandler), server.WithRefuseTrafficWithoutServiceName())
	done := make(chan error, 1)
	go func() { done <- svr.Run() }()
	defer func() { _ = svr.Stop(); <-done }()
	converters := map[string]*ToKitex{}
	for _, method := range []string{"Get", "Put", "Bad", "Slow", "Plain", "PlainOther"} {
		mappedConfig := sampleConfig(method)
		mappedConfig.HeaderMapping = map[string]string{"X-User-Id": "x-user-id", "x-device-id": "client-device"}
		worker, err := Create(method, method, mappedConfig, nil)
		if err != nil {
			t.Fatal(err)
		}
		converter := worker.(*ToKitex)
		if !dynamic {
			// Exercise the same RawBody response contract when DynamicGo is unavailable.
			p, err := generic.NewThriftContentWithAbsIncludePathProvider(mappedConfig.MainIDL, sampleFiles())
			if err != nil {
				t.Fatal(err)
			}
			g, err := thrift.NewHTTPGeneric(p)
			if err != nil {
				p.Close()
				t.Fatal(err)
			}
			cli, err := genericclient.NewClient(mappedConfig.Service, g,
				client.WithTransportProtocol(transport.TTHeader),
				client.WithMetaHandler(transmeta.ClientTTHeaderHandler))
			if err != nil {
				g.Close()
				t.Fatal(err)
			}
			converter.complete.close()
			converter.complete.client = cli
			converter.complete.close = cli.Close
		}
		defer converter.Destroy()
		converters[method] = converter
	}
	gate, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	wrappers := map[string]*response_wrap.ResponseWrap{}
	for name, status := range map[string]int{"yes": 0, "200": 200} {
		worker, err := response_wrap.Create("wrap-"+name, "wrap-"+name, &response_wrap.Config{StatusCode: status}, nil)
		if err != nil {
			t.Fatal(err)
		}
		wrappers[name] = worker.(*response_wrap.ResponseWrap)
	}
	httpServer := &fasthttp.Server{Handler: func(f *fasthttp.RequestCtx) {
		ctx := apintoctx.NewContext(f, gate.Addr().(*net.TCPAddr).Port)
		defer ctx.FastFinish()
		address := ln.Addr().String()
		if string(f.Request.Header.Peek("X-No-Upstream")) == "yes" {
			address = ""
		}
		upstreamTimeout := time.Duration(0)
		if string(f.Request.Header.Peek("X-Upstream-Timeout")) == "yes" {
			upstreamTimeout = 20 * time.Millisecond
		}
		ctx.SetBalance(testBalance{address: address, timeout: upstreamTimeout})
		method := "Get"
		switch string(f.Path()) {
		case "/manual/plain":
			method = "Plain"
		case "/manual/other":
			method = "PlainOther"
		case "/items":
			method = "Put"
		case "/bad":
			method = "Bad"
		case "/slow":
			method = "Slow"
		}
		if method == "Slow" && upstreamTimeout == 0 {
			ctx.WithValue(ctx_key.CtxKeyTimeout, 20*time.Millisecond)
		}
		// Verify the normal chain still gets control after conversion.
		invoke := chainFunc(func(_ eocontext.EoContext) error {
			return converters[method].DoFilter(ctx, chainFunc(func(c eocontext.EoContext) error {
				err := c.GetComplete().Complete(c)
				ctx.Response().SetHeader("X-Chain-Ran", "yes")
				ctx.Response().SetHeader("X-Proxy-Status", fmt.Sprint(ctx.Response().ProxyStatusCode()))
				return err
			}))
		})
		if wrapper := wrappers[string(f.Request.Header.Peek("X-Wrap"))]; wrapper != nil {
			_ = wrapper.DoHttpFilter(ctx, invoke)
		} else {
			_ = invoke(ctx)
		}
	}}
	httpDone := make(chan error, 1)
	go func() { httpDone <- httpServer.Serve(gate) }()
	defer func() { _ = httpServer.Shutdown(); <-httpDone }()
	client := &http.Client{Timeout: 3 * time.Second}
	call := func(method, path, body string, headers map[string]string, status int) string {
		t.Helper()
		req, _ := http.NewRequest(method, "http://"+gate.Addr().String()+path, strings.NewReader(body))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != status {
			t.Fatalf("%s: status %d want %d: %s", path, resp.StatusCode, status, data)
		}
		proxyStatus := status
		if headers["X-Wrap"] == "200" && path == "/bad" {
			proxyStatus = 400
		}
		if resp.Header.Get("X-Proxy-Status") != fmt.Sprint(proxyStatus) {
			t.Fatal("proxy status not recorded", resp.Header)
		}
		if resp.Header.Get("X-Chain-Ran") != "yes" {
			t.Fatal("response chain bypassed")
		}
		return string(data)
	}
	body := call("GET", "/items/9007199254740993?q=hello", "", map[string]string{"X-Trace": "trace-1"}, 200)
	if !strings.Contains(body, `"raw_id":9007199254740993`) {
		t.Fatalf("int64 precision lost: %s", body)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out["id"] != "9007199254740993" || out["q"] != "hello" || out["trace"] != "trace-1" {
		t.Fatalf("mapping lost: %v", out)
	}
	metadataBody := call("GET", "/items/1", "", map[string]string{"x-user-id": "not-a-uuid", "X-Device-ID": "device-123", "Unmapped": "secret"}, 200)
	if err := json.Unmarshal([]byte(metadataBody), &out); err != nil {
		t.Fatal(err)
	}
	metadata := out["metadata"].(map[string]interface{})
	if len(metadata) != 2 || metadata["x-user-id"] != "not-a-uuid" || metadata["client-device"] != "device-123" {
		t.Fatal("RPC metadata mapping failed", metadata)
	}
	for _, headers := range []map[string]string{nil, {"X-User-Id": ""}} {
		emptyBody := call("GET", "/items/1", "", headers, 200)
		if err := json.Unmarshal([]byte(emptyBody), &out); err != nil {
			t.Fatal(err)
		}
		if len(out["metadata"].(map[string]interface{})) != 0 {
			t.Fatal("metadata leaked into next call", out)
		}
	}
	body = call("POST", "/items", `{"message":"hello kitex"}`, map[string]string{"Content-Type": "application/json"}, 200)
	if !strings.Contains(body, `"message":"hello kitex"`) {
		t.Fatal(body)
	}
	unicodeBody := call("POST", "/items", `{"message":"中文\n\"quoted\""}`, map[string]string{"Content-Type": "application/json"}, 200)
	if err := json.Unmarshal([]byte(unicodeBody), &out); err != nil || out["message"] != "中文\n\"quoted\"" {
		t.Fatalf("JSON escaping lost: %s (%v)", unicodeBody, err)
	}
	body = call("GET", "/bad", "", nil, 400)
	if !strings.Contains(body, "400001") || !strings.Contains(body, "invalid argument") {
		t.Fatal(body)
	}
	call("GET", "/slow", "", nil, 504)
	call("GET", "/slow", "", map[string]string{"X-Upstream-Timeout": "yes"}, 504)
	for _, tt := range []struct {
		name, method, path, body, wrap string
		headers                        map[string]string
		status                         int
		code, message                  string
	}{
		{name: "raw JSON success", method: "POST", path: "/items", body: `{"message":"中文\n\"quoted\""}`, wrap: "yes", status: 200, code: "000000", message: "成功"},
		{name: "business error", method: "GET", path: "/bad", wrap: "yes", status: 400, code: "400001", message: "invalid argument"},
		{name: "business HTTP 200", method: "GET", path: "/bad", wrap: "200", status: 200, code: "400001", message: "invalid argument"},
		{name: "timeout", method: "GET", path: "/slow", wrap: "yes", status: 504, code: "504000", message: "下游服务调用超时"},
		{name: "no upstream", method: "GET", path: "/items/2", wrap: "yes", headers: map[string]string{"X-No-Upstream": "yes"}, status: 503, code: "503000", message: "无可用下游服务"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			headers := tt.headers
			if headers == nil {
				headers = map[string]string{}
			}
			headers["X-Wrap"] = tt.wrap
			headers["Content-Type"] = "application/json"
			body := call(tt.method, tt.path, tt.body, headers, tt.status)
			var result struct {
				Code, Message string
				Data          json.RawMessage
			}
			if err := json.Unmarshal([]byte(body), &result); err != nil {
				t.Fatal(err, body)
			}
			if result.Code != tt.code || result.Message != tt.message {
				t.Fatal("wrong envelope", body)
			}
			if tt.code == "000000" {
				var data struct {
					Message string
					RawID   json.Number `json:"raw_id"`
				}
				if err := json.Unmarshal(result.Data, &data); err != nil {
					t.Fatal("data must remain a JSON object", err, body)
				}
				if data.RawID.String() != "9007199254740993" || data.Message != "中文\n\"quoted\"" {
					t.Fatal("precision or escaping changed", body)
				}
			} else if string(result.Data) != "null" {
				t.Fatal("error body leaked into data", body)
			}
		})
	}
	before := calls.Load()
	call("GET", "/items/2", "", map[string]string{"X-No-Upstream": "yes"}, 503)
	call("POST", "/items", `{invalid`, nil, 400)
	if calls.Load() != before {
		t.Fatal("invalid request reached upstream")
	}
	// HTTP paths (including another method's annotation) cannot override config.
	for _, path := range []string{"/unknown", "/other"} {
		body := call("GET", path, "", nil, 200)
		if err := json.Unmarshal([]byte(body), &out); err != nil || out["rpc_method"] != "Get" {
			t.Fatalf("configured method ignored: %s (%v)", body, err)
		}
	}
	// No method or field HTTP annotations: JSON maps directly to the request
	// struct. Interleave methods sharing a client to catch method state leakage.
	for i := 0; i < 3; i++ {
		for path, method := range map[string]string{"/manual/plain": "Plain", "/manual/other": "PlainOther"} {
			body := call("POST", path+"?method=Other", `{"id":9007199254740993,"message":"无注解","tags":["a","b"]}`, map[string]string{"Content-Type": "application/json", "X-User-Id": "user-123"}, 200)
			if err := json.Unmarshal([]byte(body), &out); err != nil {
				t.Fatal(err)
			}
			if out["rpc_method"] != method || out["id"] != "9007199254740993" || out["message"] != "无注解" || len(out["tags"].([]interface{})) != 2 || out["metadata"].(map[string]interface{})["x-user-id"] != "user-123" {
				t.Fatalf("unannotated method mapping failed: %s", body)
			}
		}
	}
	// Concurrent routes share the codec/client but keep their own method binding.
	errors := make(chan error, 16)
	for i := 0; i < cap(errors); i++ {
		go func(i int) {
			path, method := "/manual/plain", "Plain"
			if i%2 != 0 {
				path, method = "/manual/other", "PlainOther"
			}
			resp, err := client.Post("http://"+gate.Addr().String()+path, "application/json", strings.NewReader(`{"id":1,"message":"concurrent","tags":[]}`))
			if err != nil {
				errors <- err
				return
			}
			defer resp.Body.Close()
			var result struct {
				Method string `json:"rpc_method"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				errors <- err
				return
			}
			if resp.StatusCode != 200 || result.Method != method {
				errors <- fmt.Errorf("concurrent call selected %s, want %s (HTTP %d)", result.Method, method, resp.StatusCode)
				return
			}
			errors <- nil
		}(i)
	}
	for i := 0; i < cap(errors); i++ {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	bad := sampleConfig("Get")
	bad.Contract = "missing@transcode"
	if converters["Get"].Reset(bad, nil) == nil {
		t.Fatal("invalid reset succeeded")
	}
	call("GET", "/items/3", "", nil, 200)
	if err := converters["Get"].Reset(sampleConfig("Get"), nil); err != nil {
		t.Fatal(err)
	}
	call("GET", "/items/4", "", nil, 200)
}

func TestSelectIDLService(t *testing.T) {
	conf := testContract(&Config{Service: "backend", IDLService: "First", Method: "GetFirst", MainIDL: "idl/api.thrift"}, map[string]string{
		"idl/api.thrift": `struct Req { 1: optional string id } struct Res { 1: optional string value }
  service First { Res GetFirst(1: Req req) (api.post="/first") }
  service Second { Res GetSecond(1: Req req) (api.post="/second") }`,
	})
	c, err := newComplete(conf)
	if err != nil {
		t.Fatal(err)
	}
	c.close()
	conf.IDLService = "Second"
	if c, err = newComplete(conf); err == nil {
		c.close()
		t.Fatal("method from different service accepted")
	}
}

func TestNamedAuthAnnotations(t *testing.T) {
	conf := testContract(&Config{Service: "backend", IDLService: "Catalog", Method: "Get", MainIDL: "idl/api.thrift"}, map[string]string{
		"idl/api.thrift": `struct Req {1: optional string id} struct Res {1: optional string value}
 service Catalog {Res Get(1: Req req) (api.get='/banner'
 api.auth.plugins='param_check_header_user_id'
 api.auth.position='header' api.auth.key='x-user-id' api.auth.mode='regex' api.auth.value='^[a-z]+$'
 api.permission.plugins='param_check_header_user_permissions'
 api.permission.position='header' api.permission.key='x-user-permission' api.permission.mode='contains' api.permission.value='user:banner:list')}`,
	})
	c, err := newComplete(conf)
	if err != nil {
		t.Fatal(err)
	}
	c.close()
}

func TestContractMethodsShareClient(t *testing.T) {
	a, err := newComplete(sampleConfig("Get"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	b, err := newComplete(sampleConfig("Put"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	if a.client != b.client {
		t.Fatal("methods rebuilt the same contract client")
	}
	a.close()
	if _, err = newComplete(sampleConfig("Put")); err != nil {
		t.Fatal("closing a route broke the shared resource", err)
	}
	cfg := sampleConfig("Get")
	if err = cfg.CheckResources(nil); err == nil {
		t.Fatal("route without an explicit dependency accepted")
	}
	requires := map[eosc.RequireId]eosc.IWorker{cfg.Contract: testContracts[string(cfg.Contract)]}
	if err = cfg.CheckResources(requires); err != nil {
		t.Fatal(err)
	}
	cfg.Method = "Missing"
	if err = cfg.CheckResources(requires); err == nil {
		t.Fatal("unknown method accepted")
	}
}
