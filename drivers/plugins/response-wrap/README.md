# 返回值包装（response_wrap）

Apinto 原生 HTTP 响应插件，驱动 ID：`eolinker.com:apinto:response_wrap`。

在「节点插件」中新建插件，名称 `response_wrap`，描述「返回值包装」，选择该驱动。设置普通「启用」并发布到集群，再在需要包装的 API 的插件配置中添加 `response_wrap`。不会自动修改现有 API，也不需要 Wire。

```json
{
  "status_code": 0,
  "success_message": "成功",
  "error_message": ""
}
```

`{}` 使用默认值。`status_code` 为 `0` 保留 HTTP 状态，为 `200` 统一返回 HTTP 200。成功码固定为字符串 `000000`。`error_message` 仅覆盖通用失败提示，不覆盖 Kitex 的业务错误消息。

成功：

```json
{"code":"000000","message":"成功","data":{"id":9007199254740993}}
```

Kitex `BizStatusError`：

```json
{"code":"401003","message":"用户身份不能为空","data":null}
```

直接读取 Kitex 的错误接口，不解析 `err.Error()` 文本。保留业务码、消息，`BizExtra` 不输出。当前转换器的业务错误 HTTP 状态为 400；可用 `status_code: 200` 覆盖。

其他失败的错误码为原 HTTP 状态码加 `000`，如 `502000` 调用失败、`503000` 无可用节点、`504000` 超时。使用中文通用提示，不把内部地址、调用堆栈或原错误响应体输出给客户端。原错误仍向插件链返回，供日志等插件处理。

成功 JSON 直接嵌入 `data`，不经过浮点反序列化，保留大整数；纯文本转为 JSON 字符串；空响应体为 `null`。插件不会猜测普通 HTTP JSON 中的业务码，也不自动拆解下游已有的包装对象。

## 执行顺序

在节点插件排序中，将 `response_wrap` 放在需要处理的校验插件和 `http_to_kitex` **之前**，使其先进入、最后处理响应；访问日志等观测插件可置于它之前。不要同时启用会覆盖整个响应体或吞掉原错误的其他响应改写插件，否则会影响包装结果和业务错误识别。

只包装已进入该插件链的响应；路由未命中时通常不会执行 API 级插件。HEAD、1xx、204、205、3xx、流式响应、SSE、附件及非 JSON/纯文本响应保持原样；状态覆盖也不作用于这些响应。包装后清除旧的 ETag、摘要和范围响应头，重新设置 Content-Type 与 Content-Length。

## Kitex RawBody

兼容 Kitex v0.16.4 的 RawBody 响应路径：转换器先将 JSON 字节写入 Apinto 响应，包装插件用 `json.RawMessage` 嵌入 `data`，不会把整个对象变成带转义的 JSON 字符串，也不会损失 int64 精度。DynamicGo 和传统泛化回退路径使用相同的包装规则。
