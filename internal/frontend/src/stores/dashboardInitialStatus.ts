import type { StatusInfo } from '../composables/useApi'

export const dashboardInitialStatusNavigationMetaKey = 'dashboardInitialStatusNavigation'

type DashboardInitialStatusNavigation = symbol

let currentNavigation: DashboardInitialStatusNavigation | undefined
let stagedStatus: StatusInfo | undefined

export function beginDashboardInitialStatusNavigation(): DashboardInitialStatusNavigation {
  const navigation = Symbol(dashboardInitialStatusNavigationMetaKey)
  currentNavigation = navigation
  stagedStatus = undefined
  return navigation
}

export function stageDashboardInitialStatus(
  navigation: DashboardInitialStatusNavigation,
  status: StatusInfo,
) {
  if (navigation !== currentNavigation) return
  stagedStatus = status
}

export function consumeDashboardInitialStatus(
  navigation: unknown,
): StatusInfo | undefined {
  if (navigation !== currentNavigation) return undefined
  const status = stagedStatus
  stagedStatus = undefined
  return status
}

export function clearDashboardInitialStatus() {
  currentNavigation = undefined
  stagedStatus = undefined
}
