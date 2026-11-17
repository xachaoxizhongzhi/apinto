package thrift

import (
	"fmt"

	"github.com/cloudwego/kitex/pkg/generic"
	"github.com/cloudwego/kitex/pkg/generic/descriptor"
)

type configuredMethodKey struct{}

// NewHTTPGeneric keeps Kitex's HTTP field conversion but selects the RPC method
// bound by Acquire. Contract providers are immutable and supply one descriptor.
func NewHTTPGeneric(provider generic.DescriptorProvider) (generic.Generic, error) {
	svc, ok := <-provider.Provide()
	if !ok || svc == nil {
		return nil, fmt.Errorf("Thrift service descriptor is unavailable")
	}
	svc.Router = &methodRouter{Router: svc.Router, service: svc}
	descriptors := make(chan *descriptor.ServiceDescriptor, 1)
	descriptors <- svc
	close(descriptors)
	return generic.HTTPThriftGeneric(&methodProvider{DescriptorProvider: provider, descriptors: descriptors}, generic.UseRawBodyForHTTPResp(true))
}

type methodProvider struct {
	generic.DescriptorProvider
	descriptors <-chan *descriptor.ServiceDescriptor
}

func (p *methodProvider) Provide() <-chan *descriptor.ServiceDescriptor { return p.descriptors }
func (p *methodProvider) Option() generic.ProviderOption {
	if options, ok := p.DescriptorProvider.(generic.GetProviderOption); ok {
		return options.Option()
	}
	return generic.ProviderOption{}
}

type methodRouter struct {
	descriptor.Router
	service *descriptor.ServiceDescriptor
}

func (r *methodRouter) Lookup(req *descriptor.HTTPRequest) (*descriptor.FunctionDescriptor, error) {
	method, _ := req.Request.Context().Value(configuredMethodKey{}).(string)
	fn, err := r.service.LookupFunctionByMethod(method)
	if err != nil {
		return nil, err
	}
	// Preserve api.path extraction when the original HTTP route matches this
	// method. An unrelated annotation must never select another RPC method or
	// contribute its path parameters to this request.
	if req.Params == nil {
		matched, err := r.Router.Lookup(req)
		if (err != nil || matched.Name != method) && req.Params != nil {
			req.Params.Recycle()
			req.Params = nil
		}
	}
	return fn, nil
}
