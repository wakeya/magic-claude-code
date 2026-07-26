// providerError.ts 把后端供应商校验返回的英文错误信息映射为当前语言的本地化文案。
//
// 背景：admin API 的校验错误（internal/config 的 Provider.Validate / Config.Validate）
// 是英文字符串，中文页面直接展示原文体验差。此工具只处理「/model 可切换模型」
// （ExposedModel）相关的校验错误，按错误文本模式匹配到 i18n 键，并把显示名等
// 关键值回显出来（便于用户发现不可见的全角空格）。无法识别的错误原样返回（兜底）。
//
// 后端错误形如：
//   provider[12]: exposed_models[0]: display name (id) "智谱　GLM" must not contain spaces or control characters (check for invisible/full-width spaces)
//   exposed model display name (id) "GLM-4.6" is duplicated between provider "A" and "B"

type TFunc = (key: string, params?: Record<string, string | number>) => string

// 各模式对应的正则与 i18n 键。name 捕获组回显触发校验的显示名。
const PATTERNS: { re: RegExp; key: string; params?: (m: RegExpMatchArray) => Record<string, string | number> }[] = [
  {
    re: /display name \(id\) "([^"]*)" must not contain spaces or control characters/,
    key: 'modal.error_exposed_spaces',
    params: (m) => ({ name: m[1] }),
  },
  {
    re: /display name \(id\) "([^"]*)" must not start with "claude-"/,
    key: 'modal.error_exposed_claude_prefix',
    params: (m) => ({ name: m[1] }),
  },
  {
    re: /display name \(id\) "([^"]*)" must not contain "\[1m]"/,
    key: 'modal.error_exposed_1m',
    params: (m) => ({ name: m[1] }),
  },
  {
    re: /display name \(id\) "([^"]*)" is reserved by Claude Code model aliases/,
    key: 'modal.error_exposed_alias',
    params: (m) => ({ name: m[1] }),
  },
  {
    re: /duplicate display name \(id\) "([^"]*)" within provider/,
    key: 'modal.error_exposed_dup_provider',
    params: (m) => ({ name: m[1] }),
  },
  {
    re: /display name \(id\) "([^"]*)" is duplicated between provider "([^"]*)" and "([^"]*)"/,
    key: 'modal.error_exposed_dup_global',
    params: (m) => ({ name: m[1], p1: m[2], p2: m[3] }),
  },
  {
    re: /exposed_models\[(\d+)]: label is required/,
    key: 'modal.error_exposed_label_required',
    params: (m) => ({ index: Number(m[1]) + 1 }), // 后端 0 起，展示 1 起
  },
  {
    re: /exposed_models\[(\d+)]: backend_model is required/,
    key: 'modal.error_exposed_backend_required',
    params: (m) => ({ index: Number(m[1]) + 1 }),
  },
]

// localizeExposedModelError 返回本地化后的错误文案；无法识别时原样返回 raw。
export function localizeExposedModelError(raw: string, t: TFunc): string {
  if (!raw) return raw
  for (const { re, key, params } of PATTERNS) {
    const m = raw.match(re)
    if (m) {
      return params ? t(key, params(m)) : t(key)
    }
  }
  return raw
}
