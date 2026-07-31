// Transient bridge for the legacy /providers/:id/usage compatibility route.
//
// The router redirect canonicalizes that URL to /?tab=providers BEFORE the
// dashboard status guard runs (so /api/status is requested only once). The
// provider id that selects which quota modal to open cannot travel through the
// cleaned URL, so the redirect stages it here and DashboardView consumes it
// exactly once on mount. This carries no auth or status data; the guard still
// verifies authentication on the canonical route, so it never masks auth.

let pendingProviderId: string | undefined

export function stagePendingUsageProvider(providerId: string): void {
  pendingProviderId = providerId
}

export function consumePendingUsageProvider(): string | undefined {
  const providerId = pendingProviderId
  pendingProviderId = undefined
  return providerId
}

export function clearPendingUsageProvider(): void {
  pendingProviderId = undefined
}
