import type { StatusInfo } from '../composables/useApi'
import {
  clearDashboardInitialStatus,
  stageDashboardInitialStatus,
} from '../stores/dashboardInitialStatus.ts'

type GuardRoute = {
  path: string
  fullPath: string
}

type StatusRequest = (input: string) => Promise<Response>

type LoginRedirect = {
  name: 'login'
  query: { redirect: string }
}

function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

export async function guardDashboardRoute(
  to: GuardRoute,
  request: StatusRequest = fetch,
  timeZone = browserTimeZone(),
): Promise<true | LoginRedirect> {
  clearDashboardInitialStatus()
  if (to.path === '/login') return true

  const redirect = to.fullPath.startsWith('/') && !to.fullPath.startsWith('//') ? to.fullPath : '/'
  try {
    const query = `?tz=${encodeURIComponent(timeZone)}`
    const res = await request(`/api/status${query}`)
    if (res.status === 401) return { name: 'login', query: { redirect } }
    if (res.ok) {
      try {
        stageDashboardInitialStatus(await res.json() as StatusInfo)
      } catch {
        // Dashboard falls back to its existing status request.
      }
    }
    return true
  } catch {
    return { name: 'login', query: { redirect } }
  }
}
