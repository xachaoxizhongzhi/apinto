# HTTP to Kitex

Apinto selects the upstream node; this converter calls the configured Thrift service and method over Kitex TTHeader. Method-level HTTP annotations are optional: the API route determines the HTTP method and path, while `idl_service` and `method` select the RPC operation. JSON request bodies map to the request struct. Existing field annotations for body, query and headers remain supported; original path parameters are extracted when the annotated route matches the configured method.

## Header metadata mapping

Add `header_mapping` to the API's existing `http_to_kitex` configuration:

```json
{
  "header_mapping": {
    "x-user-id": "x-user-id",
    "x-device-id": "client-device",
    "x-request-id": "trace-id"
  }
}
```

Keep the existing `service`, `idl_service`, `method`, `main_idl` and `contract` fields. The object above shows only the added field. Sources are HTTP header names; targets are Kitex persistent metadata keys. There are no predefined business fields. The converter does not validate identity, tokens, UUIDs or other values; configure Apinto validation plugins separately.

Only listed headers are copied. Header names are case insensitive; metadata keys are case sensitive. Header names and metadata keys must be nonempty token-form names. Duplicate source headers (including casing variants) and duplicate targets are rejected. Values are read from the proxy request after preceding plugins, without trimming or parsing. Repeated headers use the first value.

Absent or empty headers clear the mapped persistent key. Kitex metainfo cannot encode empty strings, so downstream reads these as absent/empty. The parent context is not modified. Unmapped HTTP headers are not inserted into metadata, and existing unrelated context metadata remains intact.

The downstream Kitex server must use `transmeta.ServerTTHeaderHandler`. Read values with `metainfo.GetPersistentValue(ctx, "client-device")`; persistence also enables subsequent RPC propagation when its client uses the corresponding TTHeader handler.

API plugin configuration overrides node default configuration under Apinto's existing rules. Configure mappings in the API plugin when it has its own Kitex configuration, then publish the API. Header mapping is independent of parameter validation.


## Shared contracts

The `contract` field references an immutable `thrift-<sha256>@transcode` resource containing the IDL bundle. Declare that same reference in the route's `contracts` list. API plugin configuration stores the reference and `main_idl`, not an inline copy of `thrift_files`. Routes using the same contract, main IDL, IDL service and upstream service share the parsed descriptors and Kitex client. Method selection remains request-local.

## Generic conversion

Uses Kitex v0.16.4 with its DynamicGo dependency. IDLs are supplied as content, including all included files. Both request and response conversion request DynamicGo support; provider initialization logs a warning if it falls back to standard generic conversion. DynamicGo itself may use architecture/toolchain-specific fallback implementations.

`UseRawBodyForHTTPResp(true)` returns JSON bytes in `HTTPResponse.RawBody`, also in standard generic fallback mode. The converter forwards these bytes without a map conversion or JSON string wrapping. API plugins remain responsible for validation and response transformations.

The RPC deadline uses the smaller positive route/upstream timeout. The converter records the proxy status and response duration using Apinto's response context. Calls are not retried automatically.

Kitex v0.16.4 requires a compatibility guard for TTHeader business errors with an empty result. The plugin keeps Kitex's message validation and consumes the error result as a Thrift struct, so the TTHeader handler can return the original business status instead of invoking the DynamicGo success-body decoder. Successful responses still use RawBody.
