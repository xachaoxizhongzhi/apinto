package response_wrap

import (
	"github.com/eolinker/apinto/drivers"
	"github.com/eolinker/eosc"
)

const Name = "response_wrap"

func Register(register eosc.IExtenderDriverRegister) {
	register.RegisterExtenderDriver(Name, NewFactory())
}

func NewFactory() eosc.IExtenderDriverFactory {
	return drivers.NewFactory[Config](Create)
}
