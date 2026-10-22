package web

// Admin panel API Key overview.
//
// After the SSR overview page was removed, listing and revoking live on the /api/v1/admin/api-keys* JSON endpoints (admin_keys_api.go).
// Only the page size constant is kept here, for that endpoint to reuse.

// adminKeysPageSize is the number of rows per page in the API Key overview.
const adminKeysPageSize = 50
