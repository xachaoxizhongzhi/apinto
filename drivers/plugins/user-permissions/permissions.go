// Package user_permissions validates membership in a comma-separated header.
package user_permissions

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/eolinker/apinto/drivers"
	"github.com/eolinker/eosc"
	"github.com/eolinker/eosc/eocontext"
	http_context "github.com/eolinker/eosc/eocontext/http-context"
	"golang.org/x/net/http/httpguts"
)

const Name = "user_permissions"

type Config struct {
	Position string `json:"position" label:"参数位置" enum:"header" required:"true"`
	Key      string `json:"key" label:"请求头名称" required:"true"`
	Mode     string `json:"mode" label:"匹配方式" enum:"contains,contains_all" required:"true"`
	Value    string `json:"value" label:"所需权限" required:"true"`
}

type executor struct {
	drivers.WorkerBase
	key         string
	permissions []string
}

func Register(register eosc.IExtenderDriverRegister) {
	register.RegisterExtenderDriver(Name, drivers.NewFactory[Config](Create))
}

func Create(id, name string, conf *Config, workers map[eosc.RequireId]eosc.IWorker) (eosc.IWorker, error) {
	if conf == nil || conf.Position != "header" || (conf.Mode != "contains" && conf.Mode != "contains_all") || !httpguts.ValidHeaderFieldName(conf.Key) {
		return nil, fmt.Errorf("权限校验必须指定 header、contains或contains_all 和有效请求头名称")
	}
	permission := strings.TrimSpace(conf.Value)
	if permission == "" || strings.ContainsAny(conf.Value, "\r\n") {
		return nil, fmt.Errorf("所需权限不能为空或包含换行")
	}
	permissions := strings.Split(permission, ",")
	if conf.Mode == "contains" && len(permissions) != 1 {
		return nil, fmt.Errorf("contains只能指定一个完整权限项")
	}
	for i, item := range permissions {
		permissions[i] = strings.TrimSpace(item)
		if permissions[i] == "" {
			return nil, fmt.Errorf("所需权限不能包含空项")
		}
	}
	return &executor{WorkerBase: drivers.Worker(id, name), key: conf.Key, permissions: permissions}, nil
}

func contains(value, permission string) bool {
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" && item == permission {
			return true
		}
	}
	return false
}

func (e *executor) DoFilter(ctx eocontext.EoContext, next eocontext.IChain) error {
	return http_context.DoHttpFilter(e, ctx, next)
}
func (e *executor) DoHttpFilter(ctx http_context.IHttpContext, next eocontext.IChain) error {
	value := ctx.Request().Header().GetHeader(e.key)
	for _, permission := range e.permissions {
		if !contains(value, permission) {
			ctx.Response().SetStatus(http.StatusForbidden, "Forbidden")
			ctx.Response().SetHeader("Content-Type", "text/plain; charset=utf-8")
			ctx.Response().SetBody([]byte("无权访问"))
			return nil
		}
	}
	if next != nil {
		return next.DoChain(ctx)
	}
	return nil
}
func (e *executor) CheckSkill(skill string) bool { return skill == http_context.FilterSkillName }
func (e *executor) Start() error                 { return nil }
func (e *executor) Stop() error                  { return nil }
func (e *executor) Destroy()                     {}

// The plugin manager rebuilds configured filters on publication.
func (e *executor) Reset(conf interface{}, workers map[eosc.RequireId]eosc.IWorker) error {
	return fmt.Errorf("权限配置更新需要重建插件")
}
