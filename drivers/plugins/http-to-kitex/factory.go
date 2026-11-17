package http_to_kitex

import (
	"github.com/eolinker/apinto/drivers"
	"github.com/eolinker/eosc"
)

const Name = "http_to_kitex"

func Register(register eosc.IExtenderDriverRegister) {
	register.RegisterExtenderDriver(Name, NewFactory())
}

func NewFactory() eosc.IExtenderDriverFactory {
	return drivers.NewFactory[Config](Create)
}
