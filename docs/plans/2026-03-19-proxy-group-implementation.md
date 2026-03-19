# Proxy Grouped Accounts Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 为账号增加单一代理组绑定能力，支持批量账号导入、批量代理导入、账号与代理组自动匹配，并保证账号相关流量走其绑定代理。

**Architecture:** 在 token provider 上增加账号与代理组绑定，在独立代理仓库存储代理组定义，并将最终代理 URL 注入 `Lease.Metadata`。上游聊天、OIDC refresh、catalog refresh、websearch 统一改为按 `proxy_url` 选择缓存的 HTTP client。

**Tech Stack:** Go 1.21, net/http, existing JSON state store, embedded admin HTML/JS, Dockerfile deployment, Dokploy.

---

### Task 1: Add proxy group state model and storage

**Files:**
- Create: `internal/adapters/proxyregistry/store.go`
- Test: `internal/adapters/proxyregistry/store_test.go`

**Step 1:** 定义代理组状态结构、批量导入请求、批量匹配请求和快照结构。

**Step 2:** 实现从 `data/proxy_state.json` 读取和写回。

**Step 3:** 写持久化与兼容性测试。

### Task 2: Extend provider with proxy bindings

**Files:**
- Modify: `internal/adapters/token/provider/provider.go`
- Modify: `internal/domain/account/account.go`
- Test: `internal/adapters/token/provider/provider_test.go`

**Step 1:** 给账号持久化、快照、导出和导入请求增加 `proxy_group_id`。

**Step 2:** 在 `Acquire()` 中写入 `proxy_group_id`、`proxy_group_name`、`proxy_url` 到 `Lease.Metadata`。

**Step 3:** 让 `ensureBearer()` 根据账号绑定代理选择 client。

**Step 4:** 写 provider 测试覆盖代理组绑定、批量导入和 lease metadata。

### Task 3: Add proxy-aware HTTP client pool

**Files:**
- Create: `internal/adapters/upstream/clihttp/client_pool.go`
- Test: `internal/adapters/upstream/clihttp/client_pool_test.go`
- Modify: `internal/adapters/upstream/clihttp/transport.go`

**Step 1:** 实现按 `proxy_url` 缓存 `http.Client` 的池。

**Step 2:** 保留现有默认 client，同时允许按运行时 proxy 覆盖。

**Step 3:** 为 transport 写最小测试，验证空代理、默认代理和指定代理的选择逻辑。

### Task 4: Route all upstream paths through lease proxy metadata

**Files:**
- Modify: `internal/adapters/upstream/clihttp/transport.go`
- Modify: `internal/adapters/mcp/websearch/client.go`
- Modify: `internal/adapters/catalog/runtime/catalog.go`

**Step 1:** CLI upstream 发送请求时按 `Lease.Metadata.proxy_url` 选择 client。

**Step 2:** websearch 改为按当前 lease 选 client。

**Step 3:** catalog refresh 改为按当前 lease 选 client。

**Step 4:** 写或补充测试，验证三条路径都能读到 lease 中的代理信息。

### Task 5: Extend admin API

**Files:**
- Modify: `internal/adapters/http/admin/handler.go`
- Test: `internal/adapters/http/admin/handler_test.go`

**Step 1:** 新增代理组查询、导入、启用、禁用、删除、批量匹配接口。

**Step 2:** 新增账号批量导入接口，支持数组和兼容 `clientId/clientSecret/refreshToken`。

**Step 3:** 扩展 `GET /accounts` 响应，带回代理组列展示信息。

**Step 4:** 写 handler 测试覆盖主要接口。

### Task 6: Rework admin UI

**Files:**
- Modify: `internal/bootstrap/web/admin.html`

**Step 1:** 把账号列表从卡片改为表格列展示。

**Step 2:** 增加批量账号导入面板。

**Step 3:** 增加代理组导入与列表面板。

**Step 4:** 增加账号与代理组自动匹配面板。

**Step 5:** 接通新 API，确保基础筛选和刷新逻辑继续可用。

### Task 7: Wire dependencies at bootstrap

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/bootstrap/app.go`
- Modify: `.env.example`
- Modify: `README.md`
- Modify: `docs/deployment.md`

**Step 1:** 加入代理状态文件路径配置。

**Step 2:** 在 bootstrap 中组装 proxy registry，并注入 provider/admin handler。

**Step 3:** 更新示例配置和部署说明。

### Task 8: Verify, deploy and document

**Files:**
- Modify: `Dockerfile` if needed
- Modify: `docker-compose.yml` if needed

**Step 1:** 运行相关 Go 测试。

**Step 2:** 运行最小构建检查。

**Step 3:** 扫描中文乱码特征。

**Step 4:** 提交代码。

**Step 5:** 部署到 Dokploy，配置环境与持久化卷，并验证 `/health` 和 admin/API 基本可用。
