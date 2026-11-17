package http_to_kitex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cloudwego/kitex/client/callopt"
	"github.com/cloudwego/kitex/client/genericclient"
	"github.com/cloudwego/kitex/pkg/generic"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/eolinker/apinto/entries/ctx_key"
	"github.com/eolinker/apinto/entries/router"
	"github.com/eolinker/eosc/eocontext"
	http_context "github.com/eolinker/eosc/eocontext/http-context"
)

type Complete struct {
	close         func() error
	headerMapping map[string]string
	client        genericclient.Client
	method        string
	lookup        generic.GetMethodNameByRequestFunc
}

func (c *Complete) Complete(org eocontext.EoContext) error {
	ctx, err := http_context.Assert(org)
	if err != nil {
		return err
	}
	started := time.Now()
	defer func() {
		ctx.Response().SetProxyStatus(ctx.Response().StatusCode(), "")
		ctx.Response().SetResponseTime(time.Since(started))
		ctx.SetLabel("handler", "proxy")
	}()
	proxy := ctx.Proxy()
	body, err := proxy.Body().RawBody()
	if err != nil {
		return respondError(ctx, 400, err)
	}
	if len(body) > 0 && !json.Valid(body) {
		return respondError(ctx, 400, fmt.Errorf("invalid JSON request body"))
	}
	request, err := http.NewRequestWithContext(ctx.Context(), proxy.Method(), proxy.URI().RawURL(), bytes.NewReader(body))
	if err != nil {
		return respondError(ctx, 400, err)
	}
	request.Header = proxy.Header().Headers().Clone()
	request.Host = proxy.Header().Host()
	defer request.Body.Close()
	mapped, err := generic.FromHTTPRequest(request)
	if err != nil {
		return respondError(ctx, 400, err)
	}
	// Bind the configured method before Kitex resolves and serializes the request.
	// HTTP annotations may supply field mappings, but cannot select another RPC.
	method, err := c.lookup(mapped)
	if err != nil || method != c.method {
		return respondError(ctx, 400, fmt.Errorf("HTTP request does not match Kitex method %q", c.method))
	}
	balance := ctx.GetBalance()
	if balance == nil {
		return respondError(ctx, 503, fmt.Errorf("no upstream configured"))
	}
	node, _, err := balance.Select(ctx)
	if err != nil {
		return respondError(ctx, 503, err)
	}
	if node == nil || node.Addr() == "" {
		return respondError(ctx, 503, fmt.Errorf("no upstream available"))
	}
	timeout, ok := ctx.Value(ctx_key.CtxKeyTimeout).(time.Duration)
	if !ok || timeout <= 0 {
		timeout = router.DefaultTimeout
	}
	if upstreamTimeout := balance.TimeOut(); upstreamTimeout > 0 && upstreamTimeout < timeout {
		timeout = upstreamTimeout
	}
	// Apinto owns discovery and node selection. Do not retry an RPC that may
	// already have changed downstream state.
	result, err := c.client.GenericCall(c.rpcContext(ctx.Context(), request.Header), c.method, mapped,
		callopt.WithHostPort(node.Addr()), callopt.WithRPCTimeout(timeout))
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) || kerrors.IsTimeoutError(err) {
			status = http.StatusGatewayTimeout
		} else if _, biz := kerrors.FromBizStatusError(err); biz || errors.Is(err, kerrors.ErrPayloadValidation) {
			status = http.StatusBadRequest
		}
		return respondError(ctx, status, err)
	}
	response, ok := result.(*generic.HTTPResponse)
	if !ok || response == nil {
		return respondError(ctx, 502, fmt.Errorf("invalid Kitex HTTP response"))
	}
	// RawBody contains JSON bytes in both DynamicGo and standard fallback mode.
	// Forward them without decoding into maps or encoding JSON as a string.
	encoded := response.RawBody
	status := int(response.StatusCode)
	if status == 0 {
		status = http.StatusOK
	}
	for key, values := range response.Header {
		for _, value := range values {
			ctx.Response().AddHeader(key, value)
		}
	}
	ctx.Response().SetStatus(status, http.StatusText(status))
	ctx.Response().SetHeader("Content-Type", "application/json")
	ctx.Response().SetBody(encoded)
	return nil
}

func respondError(ctx http_context.IHttpContext, status int, err error) error {
	ctx.Response().SetStatus(status, http.StatusText(status))
	ctx.Response().SetHeader("Content-Type", "text/plain; charset=utf-8")
	ctx.Response().SetBody([]byte(err.Error()))
	return err
}
