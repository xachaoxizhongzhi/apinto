# 请求头权限校验

驱动 ID：`eolinker.com:apinto:user_permissions`。在控制台节点插件中新建同名实例 `user_permissions`，每个使用它的集群保存配置，设为普通“启用”并发布。

```json
{
  "position": "header",
  "key": "x-user-permission",
  "mode": "contains",
  "value": "user:banner:list"
}
```

读取指定请求头，按英文逗号分割，去除每项首尾空白，忽略空项，区分大小写进行完整权限项匹配。缺失、空值、子串、不同大小写及通配符不能代替所需权限。`contains` 的 `value` 只能指定一个非空权限项。`contains_all` 的 `value` 使用英文逗号列出多个权限，必须全部满足；每项去除首尾空白后不能为空。

通过后继续插件链；失败返回 HTTP 403 和“无权访问”，中断后续调用。位于前面的 response_wrap 使用默认配置时会包装为 `{"code":"403000","message":"无权访问","data":null}`。该插件不解析请求体、不验证 Token、不读数据库，也不写 Kitex 元信息。

API 的具体权限配置覆盖集群默认配置。多个接口可复用同一个命名实例并要求不同权限。不要设为全局启用，除非确实希望未显式声明它的 API 也执行默认权限检查。

推荐发布顺序：response_wrap → param_check_header_user_id → user_permissions → http_to_kitex。权限头应由可信的前置身份验证环节生成。

## 同时要求多个权限

```json
{
  "position": "header",
  "key": "x-user-permission",
  "mode": "contains_all",
  "value": "user:banner:list,user:banner:update"
}
```

例如请求头为 `user:banner:list,user:banner:update,user:banner:get` 时通过；只有 `user:banner:list` 时拒绝。请求头名称由 `key` 配置，不限定为示例名称。
