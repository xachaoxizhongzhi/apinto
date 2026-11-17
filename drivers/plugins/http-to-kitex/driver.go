package http_to_kitex

import (
	"fmt"
	"sync"

	"github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/transmeta"
	"github.com/cloudwego/kitex/transport"
	"github.com/eolinker/apinto/drivers"
	thrift "github.com/eolinker/apinto/drivers/transcode/thrift"
	"github.com/eolinker/eosc"
	"github.com/eolinker/eosc/common/bean"
)

var resourceWorkers eosc.IWorkers
var resourceOnce sync.Once

func Create(id, name string, conf *Config, workers map[eosc.RequireId]eosc.IWorker) (eosc.IWorker, error) {
	complete, err := newComplete(conf, workers)
	if err != nil {
		return nil, err
	}
	return &ToKitex{WorkerBase: drivers.Worker(id, name), complete: complete}, nil
}

// CheckResources is called before the router replaces its active configuration.
func (c *Config) CheckResources(workers map[eosc.RequireId]eosc.IWorker) error {
	if workers[c.Contract] == nil {
		return fmt.Errorf("Thrift contract %s must be declared in router contracts", c.Contract)
	}
	complete, err := newComplete(c, workers)
	if err != nil {
		return err
	}
	return complete.close()
}

func newComplete(conf *Config, requires ...map[eosc.RequireId]eosc.IWorker) (*Complete, error) {
	if conf == nil || conf.Service == "" || conf.Method == "" || conf.MainIDL == "" || conf.Contract == "" {
		return nil, fmt.Errorf("service, method, main_idl and contract are required")
	}
	mapping, err := validateHeaderMapping(conf.HeaderMapping)
	if err != nil {
		return nil, err
	}
	var worker eosc.IWorker
	if len(requires) > 0 {
		worker = requires[0][conf.Contract]
	}
	// PluginManager creates filters without the route's dependency map. The route
	// declares the dependency separately; resolve the same worker as http_to_grpc.
	if worker == nil {
		resourceOnce.Do(func() { bean.Autowired(&resourceWorkers) })
		if resourceWorkers != nil {
			worker, _ = resourceWorkers.Get(string(conf.Contract))
		}
	}
	resource, ok := worker.(*thrift.Worker)
	if !ok {
		return nil, fmt.Errorf("Thrift contract %s is unavailable", conf.Contract)
	}
	cli, lookup, release, err := resource.Acquire(conf.MainIDL, conf.IDLService, conf.Service, conf.Method,
		client.WithTransportProtocol(transport.TTHeader), client.WithPayloadCodec(newBusinessErrorCodec()), client.WithMetaHandler(transmeta.ClientTTHeaderHandler))
	if err != nil {
		return nil, err
	}
	return &Complete{client: cli, method: conf.Method, lookup: lookup, headerMapping: mapping, close: release}, nil
}
