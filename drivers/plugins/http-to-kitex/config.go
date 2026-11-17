package http_to_kitex

import "github.com/eolinker/eosc"

// Contract references an immutable transcode resource shared by all methods.
type Config struct {
	HeaderMapping map[string]string `json:"header_mapping,omitempty" label:"HTTP请求头到Kitex元信息映射"`
	IDLService    string            `json:"idl_service,omitempty" label:"Thrift Service名称"`
	Service       string            `json:"service" label:"目标服务名" required:"true"`
	Method        string            `json:"method" label:"方法名称" required:"true"`
	MainIDL       string            `json:"main_idl" label:"入口Thrift文件" required:"true"`
	Contract      eosc.RequireId    `json:"contract" label:"Thrift契约资源" required:"true" skill:"github.com/eolinker/apinto/transcode.thrift.Contract"`
}
