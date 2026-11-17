// Package response_wrap implements 返回值包装 for buffered HTTP and Kitex responses.
package response_wrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/eolinker/apinto/drivers"
	"github.com/eolinker/eosc"
	"github.com/eolinker/eosc/eocontext"
	http_context "github.com/eolinker/eosc/eocontext/http-context"
)

type wrappedKey struct{}

type envelope struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

type ResponseWrap struct {
	drivers.WorkerBase
	mu     sync.RWMutex
	config Config
}

var _ eocontext.IFilter = (*ResponseWrap)(nil)
var _ http_context.HttpFilter = (*ResponseWrap)(nil)

func Create(id, name string, conf *Config, workers map[eosc.RequireId]eosc.IWorker) (eosc.IWorker, error) {
	if err := Check(conf); err != nil {
		return nil, err
	}
	return &ResponseWrap{WorkerBase: drivers.Worker(id, name), config: *conf}, nil
}

func (p *ResponseWrap) DoFilter(ctx eocontext.EoContext, next eocontext.IChain) error {
	return http_context.DoHttpFilter(p, ctx, next)
}

func (p *ResponseWrap) DoHttpFilter(ctx http_context.IHttpContext, next eocontext.IChain) error {
	p.mu.RLock()
	conf := p.config
	p.mu.RUnlock()
	var err error
	if next != nil {
		err = next.DoChain(ctx)
	}
	response := ctx.Response()
	if ctx.Value(wrappedKey{}) != nil {
		return err
	}
	if err == nil {
		err = response.ResponseError()
	}
	status := response.StatusCode()
	contentType := strings.ToLower(strings.Split(response.ContentType(), ";")[0])
	// These responses cannot safely carry a JSON envelope.
	if ctx.Request().Method() == http.MethodHead || status < 200 || status >= 300 && status < 400 || status == 204 || status == 205 || response.IsBodyStream() || strings.Contains(strings.ToLower(response.GetHeader("Content-Disposition")), "attachment") {
		return err
	}
	if contentType != "" && contentType != "application/json" && !strings.HasSuffix(contentType, "+json") && contentType != "text/plain" {
		return err
	}
	result := envelope{Code: "000000", Message: conf.SuccessMessage}
	if result.Message == "" {
		result.Message = "成功"
	}
	if biz, ok := kerrors.FromBizStatusError(err); ok {
		result.Code = strconv.FormatInt(int64(biz.BizStatusCode()), 10)
		result.Message = biz.BizMessage()
		if status < 400 {
			status = http.StatusBadRequest
		}
	} else if err != nil || status >= 400 {
		if status < 400 {
			status = http.StatusBadGateway
			if errors.Is(err, context.DeadlineExceeded) || kerrors.IsTimeoutError(err) {
				status = http.StatusGatewayTimeout
			}
		}
		result.Code = strconv.Itoa(status) + "000"
		result.Message = conf.ErrorMessage
		if result.Message == "" {
			result.Message = errorMessage(status)
		}
	} else {
		body := response.GetBody()
		// GetBody decodes supported compression. Never wrap still-encoded bytes.
		if response.GetHeader("Content-Encoding") != "" {
			return err
		}
		if len(body) > 0 {
			if json.Valid(body) {
				result.Data = json.RawMessage(body)
			} else {
				result.Data = string(body)
			}
		}
	}
	encoded, encodeErr := json.Marshal(result)
	if encodeErr != nil {
		return encodeErr
	}
	if conf.StatusCode != 0 {
		status = conf.StatusCode
	}
	response.SetStatus(status, http.StatusText(status))
	for _, key := range []string{"Content-Encoding", "ETag", "Content-MD5", "Digest", "Content-Range", "Accept-Ranges"} {
		response.DelHeader(key)
	}
	response.SetHeader("Content-Type", "application/json; charset=utf-8")
	response.SetBody(encoded)
	ctx.WithValue(wrappedKey{}, true)
	// Preserve error propagation for upstream logging and other response plugins.
	return err
}

func errorMessage(status int) string {
	switch status {
	case 400:
		return "请求参数错误"
	case 401:
		return "未通过身份验证"
	case 403:
		return "无权访问"
	case 404:
		return "资源不存在"
	case 429:
		return "请求过于频繁"
	case 502:
		return "下游服务调用失败"
	case 503:
		return "无可用下游服务"
	case 504:
		return "下游服务调用超时"
	default:
		return "服务调用失败"
	}
}

func (p *ResponseWrap) Reset(value interface{}, workers map[eosc.RequireId]eosc.IWorker) error {
	conf, err := drivers.Assert[Config](value)
	if err != nil {
		return err
	}
	if err = Check(conf); err != nil {
		return err
	}
	p.mu.Lock()
	p.config = *conf
	p.mu.Unlock()
	return nil
}
func (p *ResponseWrap) Start() error                 { return nil }
func (p *ResponseWrap) Stop() error                  { return nil }
func (p *ResponseWrap) Destroy()                     {}
func (p *ResponseWrap) CheckSkill(skill string) bool { return skill == http_context.FilterSkillName }
