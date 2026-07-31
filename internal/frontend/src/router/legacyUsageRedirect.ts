import type { RouteLocationRaw } from 'vue-router'
import { stagePendingUsageProvider } from '../stores/pendingUsageProvider.ts'

// Canonicalizes the legacy /providers/:id/usage compatibility URL to the
// dashboard route BEFORE the status guard fetches /api/status. The provider id
// is staged for DashboardView (see pendingUsageProvider) instead of riding in
// the query, which avoids the second status request that the old
// query-strip-and-replace flow triggered.
export function resolveLegacyUsageRedirect(to: {
  params: Record<string, unknown>
  query: Record<string, unknown>
  hash: string
}): RouteLocationRaw {
  stagePendingUsageProvider(String(to.params.providerId))
  return {
    path: '/',
    query: { ...to.query, tab: 'providers' },
    hash: to.hash,
  }
}
