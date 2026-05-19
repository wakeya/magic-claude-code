export const USAGE_COVERAGE_TOOLTIP = 'Usage 覆盖率 = 成功解析到 usage 的请求数 / 请求总数。已补账的 Claude session 日志会计入成功解析；缺少 usage、解析失败或非 2xx 请求会降低覆盖率。'

export function formatPercent(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '0.0000%'
  return `${(value * 100).toFixed(4)}%`
}
