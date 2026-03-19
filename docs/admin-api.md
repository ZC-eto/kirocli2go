# Admin API

所有 `admin API` 都要求：

- Header: `Authorization: Bearer <KIROCLI_GO_API_TOKEN>`

基准路径：

- `/admin/api`

## 读取接口

### `GET /admin/api/version`

返回版本号。

### `GET /admin/api/config`

返回当前生效的非敏感配置视图。

### `GET /admin/api/doctor`

返回最小诊断结果：

- 状态文件路径检查
- 账号来源是否配置
- 后台任务是否启用

### `GET /admin/api/status`

返回：

- 账号数
- 活跃账号数
- `by_status`
- 累积 stats

### `GET /admin/api/accounts`

返回当前账号快照：

- `id`
- `source`
- `status`
- `weight`
- `disabled`
- `has_bearer`
- `has_refresh`
- `expires_at`
- `cooldown_until`
- `last_used_at`
- `last_error`
- `failures`
- `proxy_group_id`
- `proxy_group_name`
- `proxy_url_masked`

### `GET /admin/api/proxy-groups`

返回代理组列表：

- `available`
- `groups[]`
- `groups[].id`
- `groups[].name`
- `groups[].proxy_url_masked`
- `groups[].enabled`
- `groups[].notes`
- `groups[].bound_account_count`
- `groups[].created_at`
- `groups[].updated_at`

说明：

- 当后端代理组仓库尚未接入时，`available` 可能为 `false`
- 读取接口仍可返回空列表，写接口会返回 `501 Not Implemented`

### `GET /admin/api/models`

返回：

- 运行时模型目录快照
- 当前客户端可见模型列表

### `GET /admin/api/export`

导出当前账号池视图，包含：

- managed 账号
- 外部来源账号的运行时状态与 override

### `GET /admin/api/request-logs`

查询参数：

- `limit`
- `offset`
- `protocol`
- `endpoint`
- `model`
- `account_id`
- `success`
- `failure_reason`

## 写接口

### `POST /admin/api/accounts/import`

请求体：

```json
{
  "id": "optional-id",
  "weight": 100,
  "bearer_token": "",
  "refresh_token": "",
  "client_id": "",
  "client_secret": "",
  "proxy_group_id": ""
}
```

规则：

- `bearer_token` 和 `refresh_token` 至少提供一个
- 若只给 `refresh_token`，服务会尝试刷新出 bearer

### `POST /admin/api/accounts/import/bulk`

批量导入账号，兼容以下输入之一，服务端会归一化后调用 `ImportAccountsBulk`：

```json
{
  "content": "[{\"id\":\"optional-id\",\"weight\":100,\"bearer_token\":\"\",\"refresh_token\":\"\",\"client_id\":\"\",\"client_secret\":\"\",\"proxy_group_id\":\"\"}]",
  "default_weight": 100,
  "proxy_group_id": ""
}
```

常见兼容字段：

- `clientId`
- `clientSecret`
- `refreshToken`
- `proxyGroupId`
- `email`
- `content`

如果仍然提交 `accounts` / `items` / 直接数组 / 每行 JSON，服务端也会转换成 `content` 再交给后端。

### `POST /admin/api/proxy-groups/import`

批量导入代理组，优先建议使用结构化请求，这样可以保留你指定的 `id/name/notes/enabled`：

```json
{
  "groups": [
    {
      "id": "proxy-us-01",
      "name": "US-01",
      "proxy_url": "http://user:pass@74.81.81.81:10000",
      "notes": "group-a",
      "enabled": true
    }
  ],
  "name_prefix": "proxy"
}
```

也兼容 provider 的纯文本契约：

```json
{
  "content": "http://user:pass@1.2.3.4:10000\nhttp://user:pass@1.2.3.4:10001",
  "name_prefix": "proxy"
}
```

兼容以下输入之一，服务端会在 admin 层归一化为 provider 请求：

- `{"groups":[...]}`
- 直接 JSON 数组 `[...]`
- 每行一个代理 URL，或 `name|proxy_url` / `id|name|proxy_url`

请求体示例：

```json
{
  "content": "http://user:pass@1.2.3.4:10000",
  "name_prefix": "proxy"
}
```

### `POST /admin/api/accounts/{id}/enable`

启用账号。

### `POST /admin/api/accounts/{id}/disable`

禁用账号。

### `DELETE /admin/api/accounts/{id}`

删除账号：

- `managed` 账号会被真正移除
- 外部来源账号会被标记禁用

### `POST /admin/api/accounts/{id}/refresh`

手动刷新指定账号 bearer/token 过期状态。

### `POST /admin/api/accounts/{id}/weight`

请求体：

```json
{
  "weight": 250
}
```

### `POST /admin/api/proxy-groups/{id}/enable`

启用代理组。

### `POST /admin/api/proxy-groups/{id}/disable`

禁用代理组。

### `DELETE /admin/api/proxy-groups/{id}`

删除代理组。

### `POST /admin/api/proxy-groups/assign`

把账号分配给代理组。

请求体：

```json
{
  "account_ids": ["acct-1", "acct-2"],
  "proxy_group_ids": ["proxy-us-01", "proxy-us-02"],
  "accounts_per_proxy": 5
}
```

说明：

- `account_ids` 优先使用显式传入的账号列表
- `proxy_group_ids` 优先使用显式传入的代理组列表
- `accounts_per_proxy` 表示按顺序每个代理组分配多少个账号

### `POST /admin/api/models/refresh`

立即触发一次模型目录刷新。
