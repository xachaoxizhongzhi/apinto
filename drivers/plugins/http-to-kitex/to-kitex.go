package http_to_kitex

import (
	"fmt"
	"sync"

	"github.com/eolinker/apinto/drivers"
	"github.com/eolinker/eosc"
	"github.com/eolinker/eosc/eocontext"
	http_context "github.com/eolinker/eosc/eocontext/http-context"
)

var _ eocontext.IFilter = (*ToKitex)(nil)
var _ http_context.HttpFilter = (*ToKitex)(nil)

type ToKitex struct {
	drivers.WorkerBase
	mu       sync.RWMutex
	complete *Complete
}

func (p *ToKitex) DoFilter(ctx eocontext.EoContext, next eocontext.IChain) error {
	return http_context.DoHttpFilter(p, ctx, next)
}

func (p *ToKitex) DoHttpFilter(ctx http_context.IHttpContext, next eocontext.IChain) error {
	// Keep the shared client alive until this request chain finishes.
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.complete == nil {
		return respondError(ctx, 503, fmt.Errorf("Kitex converter is stopped"))
	}
	ctx.SetCompleteHandler(p.complete)
	if next != nil {
		return next.DoChain(ctx)
	}
	return nil
}

func (p *ToKitex) Start() error                 { return nil }
func (p *ToKitex) Stop() error                  { return nil }
func (p *ToKitex) CheckSkill(skill string) bool { return skill == http_context.FilterSkillName }

func (p *ToKitex) Reset(v interface{}, workers map[eosc.RequireId]eosc.IWorker) error {
	conf, err := drivers.Assert[Config](v)
	if err != nil {
		return err
	}
	next, err := newComplete(conf, workers)
	if err != nil {
		return err
	}
	p.mu.Lock()
	old := p.complete
	if old == nil {
		p.mu.Unlock()
		_ = next.close()
		return fmt.Errorf("Kitex converter is destroyed")
	}
	p.complete = next
	p.mu.Unlock()
	return old.close()
}

func (p *ToKitex) Destroy() {
	p.mu.Lock()
	old := p.complete
	p.complete = nil
	p.mu.Unlock()
	if old != nil {
		_ = old.close()
	}
}
