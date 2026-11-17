package response_wrap

import "fmt"

type Config struct {
	StatusCode     int    `json:"status_code" label:"HTTP响应状态码" description:"0保留原状态码，200统一返回HTTP 200"`
	SuccessMessage string `json:"success_message" label:"成功提示" description:"默认：成功"`
	ErrorMessage   string `json:"error_message" label:"通用错误提示" description:"留空使用状态码对应的中文提示，不覆盖Kitex业务错误消息"`
}

func Check(conf *Config) error {
	if conf == nil {
		return fmt.Errorf("返回值包装配置不能为空")
	}
	if conf.StatusCode != 0 && conf.StatusCode != 200 {
		return fmt.Errorf("HTTP响应状态码只能为0或200")
	}
	return nil
}
