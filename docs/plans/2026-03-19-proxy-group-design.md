# Proxy Grouped Accounts Design

**Date:** 2026-03-19

**Goal**

为 `kirocli-go` 增加代理组能力，使单个账号只能绑定一个代理组、单个代理组可绑定多个账号，并保证账号后续的 token refresh、聊天上游请求、模型目录刷新、MCP web search 都走该账号所属代理组的代理。

**Scope**

- 批量导入账号
- 批量导入代理组
- 账号与代理组批量匹配
- 管理页账号按列展示
- Dokploy 部署配置补齐

## Data Model

新增独立代理状态文件 `data/proxy_state.json`，由专门的代理仓库负责读写。

代理组模型：

- `id`
- `name`
- `proxy_url`
- `enabled`
- `notes`
- `bound_account_count`
- `created_at`
- `updated_at`

账号模型扩展：

- `proxy_group_id`

该字段进入：

- managed 账号持久化
- override 持久化
- admin 快照
- 导出结构
- 批量导入请求

## Runtime Routing

运行时规则：

1. 账号绑定代理组时，优先使用该组的 `proxy_url`
2. 账号未绑定代理组时，回退到全局 `KIROCLI_GO_PROXY_URL`
3. 若两者都为空，则直连

`Provider.Acquire()` 负责把最终选中的代理信息写入 `account.Lease.Metadata`：

- `proxy_group_id`
- `proxy_group_name`
- `proxy_url`

消费链路统一按 `Lease.Metadata.proxy_url` 选择 HTTP client：

- CLI upstream chat
- OIDC refresh
- runtime catalog refresh
- MCP web search

底层采用按 `proxy_url` 缓存的 client/transport 池，避免重复构建 transport。

## Admin API

保留现有单账号导入接口，新增：

- `POST /admin/api/accounts/import/bulk`
- `GET /admin/api/proxy-groups`
- `POST /admin/api/proxy-groups/import`
- `POST /admin/api/proxy-groups/{id}/enable`
- `POST /admin/api/proxy-groups/{id}/disable`
- `DELETE /admin/api/proxy-groups/{id}`
- `POST /admin/api/proxy-groups/assign`

批量匹配接口支持：

- 显式账号到代理组映射
- 按顺序自动分配，参数 `accounts_per_proxy`

## Admin UI

账号区改为列式表格，显示：

- ID
- source
- status
- weight
- proxy group
- in_pool
- has_refresh
- expires_at
- last_used_at
- last_refresh_at
- failures
- last_error

管理页新增两块：

1. 批量导入账号
   - 支持 JSON 数组
   - 支持 `clientId/clientSecret/refreshToken` 到内部字段的转换
   - 支持导入时直接绑定 `proxy_group_id`

2. 代理组管理
   - 批量导入代理 URL
   - 查看代理组列表
   - 批量匹配账号与代理组

## Compatibility

- 旧版 `accounts_state.json` 没有 `proxy_group_id` 时默认视为未绑定
- 没有 `proxy_state.json` 时使用空代理组列表
- 旧版管理页接口保持兼容

## Testing

至少覆盖：

- provider 的代理组持久化与批量导入
- provider 根据账号绑定返回正确 lease metadata
- OIDC refresh 按账号代理走对应 transport
- cli upstream/websearch/catalog 根据 lease metadata 选 client
- admin API 批量导入与批量匹配
- 前端基础渲染和交互通过最小冒烟检查

## Deployment

Dokploy 继续使用现有 `Dockerfile` 部署。

部署要求：

- 持久化 `/app/data`
- 健康检查 `/health`
- 保留原有环境变量
- 新增代理状态文件落盘，无需额外服务依赖
