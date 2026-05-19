export function formatPercent(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '0.0000%'
  return `${(value * 100).toFixed(4)}%`
}
