import type { StatusInfo } from '../composables/useApi'

let stagedStatus: StatusInfo | undefined

export function stageDashboardInitialStatus(status: StatusInfo) {
  stagedStatus = status
}

export function consumeDashboardInitialStatus(): StatusInfo | undefined {
  const status = stagedStatus
  stagedStatus = undefined
  return status
}

export function clearDashboardInitialStatus() {
  stagedStatus = undefined
}
