package web

// 管理面板 API Key 总览（DESIGN.md §8.4；ROADMAP.md M6-9）。
//
// SSR 总览页删除后，列表与撤销在 /api/v1/admin/api-keys* 的 JSON 端点上（spa_admin_keys.go）。
// 这里只保留分页大小常量，供该端点复用。

// adminKeysPageSize 是 API Key 总览每页行数。
const adminKeysPageSize = 50
