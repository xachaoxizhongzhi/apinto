// Package thrift stores one immutable IDL bundle for all referencing routes.
package thrift

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/client/genericclient"
	"github.com/cloudwego/kitex/pkg/generic"
	"github.com/cloudwego/thriftgo/parser"
	"github.com/eolinker/apinto/drivers"
	"github.com/eolinker/eosc"
)

const Skill = "github.com/eolinker/apinto/transcode.thrift.Contract"

type Config struct {
	Revision string            `json:"revision" required:"true"`
	Files    map[string]string `json:"thrift_files" required:"true"`
}

type clientKey struct{ Main, IDLService, Service string }
type sharedClient struct {
	Client genericclient.Client
	Codec  generic.Generic
	refs   int
}

type Worker struct {
	drivers.WorkerBase
	mu       sync.Mutex
	revision string
	files    map[string]string
	clients  map[clientKey]*sharedClient
	stopped  bool
}

func Register(register eosc.IExtenderDriverRegister) {
	register.RegisterExtenderDriver("thrift_transcode", drivers.NewFactory[Config](Create))
}

func validate(c *Config) error {
	if c == nil || len(c.Files) == 0 || len(c.Files) > 256 {
		return fmt.Errorf("Thrift contract must contain 1 to 256 files")
	}
	raw, err := json.Marshal(c.Files)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	if c.Revision != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("Thrift contract checksum mismatch")
	}
	total := 0
	includes := map[string][]string{}
	for name, content := range c.Files {
		if path.IsAbs(name) || path.Clean(name) != name || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00") || path.Ext(name) != ".thrift" || !utf8.ValidString(content) {
			return fmt.Errorf("invalid Thrift file %q", name)
		}
		total += len(content)
		if total > 32<<20 {
			return fmt.Errorf("Thrift contract exceeds 32 MiB")
		}
		ast, err := parser.ParseString(name, content)
		if err != nil {
			return fmt.Errorf("invalid Thrift file %s: %w", name, err)
		}
		for _, inc := range ast.Includes {
			target := path.Join(path.Dir(name), inc.Path)
			if path.IsAbs(inc.Path) || strings.ContainsAny(inc.Path, "\\\x00") || strings.HasPrefix(target, "../") {
				return fmt.Errorf("invalid include in %s", name)
			}
			if _, ok := c.Files[target]; !ok {
				return fmt.Errorf("missing include %s in %s", inc.Path, name)
			}
			includes[name] = append(includes[name], target)
		}
	}
	heights := map[string]int{}
	visiting := map[string]bool{}
	var walk func(string) (int, error)
	walk = func(name string) (int, error) {
		if visiting[name] {
			return 0, fmt.Errorf("cyclic Thrift includes: %s", name)
		}
		if height := heights[name]; height > 0 {
			return height, nil
		}
		visiting[name] = true
		defer delete(visiting, name)
		height := 1
		for _, next := range includes[name] {
			child, err := walk(next)
			if err != nil {
				return 0, err
			}
			if child+1 > height {
				height = child + 1
			}
		}
		if height > 33 {
			return 0, fmt.Errorf("Thrift include depth exceeds 32: %s", name)
		}
		heights[name] = height
		return height, nil
	}
	for name := range c.Files {
		if _, err := walk(name); err != nil {
			return err
		}
	}
	return nil
}

func Create(id, name string, c *Config, _ map[eosc.RequireId]eosc.IWorker) (eosc.IWorker, error) {
	if err := validate(c); err != nil {
		return nil, err
	}
	if name != "thrift-"+c.Revision {
		return nil, fmt.Errorf("Thrift resource name must match its revision")
	}
	files := make(map[string]string, len(c.Files))
	for name, content := range c.Files {
		files[name] = content
	}
	return &Worker{WorkerBase: drivers.Worker(id, name), revision: c.Revision, files: files, clients: map[clientKey]*sharedClient{}}, nil
}

func (w *Worker) Start() error                 { return nil }
func (w *Worker) CheckSkill(skill string) bool { return skill == Skill }
func (w *Worker) Reset(v interface{}, _ map[eosc.RequireId]eosc.IWorker) error {
	c, err := drivers.Assert[Config](v)
	if err != nil {
		return err
	}
	if err = validate(c); err != nil {
		return err
	}
	if c.Revision != w.revision {
		return fmt.Errorf("Thrift contract is immutable; create a new resource")
	}
	return nil
}
func (w *Worker) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = true
	for key, shared := range w.clients {
		if shared.refs == 0 {
			_ = shared.Client.Close()
			delete(w.clients, key)
		}
	}
	return nil
}

// Acquire shares the provider, codec and client across methods. Options are supplied
// by the Kitex converter and identical for every user of this resource.
func (w *Worker) Acquire(main, idlService, service, method string, opts ...client.Option) (cli genericclient.Client, lookup generic.GetMethodNameByRequestFunc, release func() error, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return nil, nil, nil, fmt.Errorf("Thrift resource is stopped")
	}
	key := clientKey{main, idlService, service}
	shared := w.clients[key]
	if shared == nil {
		shared, err = w.build(key, opts)
		if err != nil {
			return nil, nil, nil, err
		}
		w.clients[key] = shared
	}
	if shared.Codec.GenericMethod()(context.Background(), method) == nil {
		return nil, nil, nil, fmt.Errorf("method %q is not in the IDL", method)
	}
	codecLookup, ok := shared.Codec.GetExtra("get_method_name_by_request_func").(generic.GetMethodNameByRequestFunc)
	if !ok {
		return nil, nil, nil, fmt.Errorf("Kitex HTTP method lookup unavailable")
	}
	shared.refs++
	var once sync.Once
	release = func() error {
		var closeErr error
		once.Do(func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			shared.refs--
			if w.stopped && shared.refs == 0 {
				closeErr = shared.Client.Close()
				delete(w.clients, key)
			}
		})
		return closeErr
	}
	// Bind the configured method to this request, never to the shared client.
	boundLookup := func(value interface{}) (string, error) {
		req, ok := value.(*generic.HTTPRequest)
		if !ok || req == nil || req.Request == nil {
			return "", fmt.Errorf("Kitex requires an HTTP request")
		}
		req.Request = req.Request.WithContext(context.WithValue(req.Request.Context(), configuredMethodKey{}, method))
		return codecLookup(req)
	}
	return shared.Client, boundLookup, release, nil
}

func (w *Worker) build(key clientKey, opts []client.Option) (out *sharedClient, err error) {
	if w.files[key.Main] == "" {
		return nil, fmt.Errorf("main_idl %q is missing from contract", key.Main)
	}
	var cleanup func() error
	defer func() {
		if value := recover(); value != nil {
			out = nil
			err = fmt.Errorf("invalid Kitex HTTP contract: %v", value)
		}
		if err != nil && cleanup != nil {
			_ = cleanup()
		}
	}()
	var options []generic.ThriftIDLProviderOption
	if key.IDLService != "" {
		options = append(options, generic.WithIDLServiceName(key.IDLService))
	}
	provider, err := generic.NewThriftContentWithAbsIncludePathProviderWithDynamicGo(key.Main, w.files, options...)
	if err != nil {
		return nil, err
	}
	cleanup = provider.Close
	codec, err := NewHTTPGeneric(provider)
	if err != nil {
		return nil, err
	}
	cleanup = codec.Close
	cli, err := genericclient.NewClient(key.Service, codec, opts...)
	if err != nil {
		return nil, err
	}
	return &sharedClient{Client: cli, Codec: codec}, nil
}
