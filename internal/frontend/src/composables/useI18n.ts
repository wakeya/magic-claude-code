import { ref, watch } from 'vue'

export type Locale = 'zh' | 'en'

const stored = (typeof localStorage !== 'undefined' && localStorage.getItem('locale')) as Locale | null
const locale = ref<Locale>(stored === 'en' ? 'en' : 'zh')

watch(locale, (v) => {
  localStorage.setItem('locale', v)
})

const messages: Record<Locale, Record<string, string>> = {
  zh: {
    // Login
    'login.subtitle': '输入管理员密码以继续',
    'login.password': '密码',
    'login.password_placeholder': '请输入密码',
    'login.submit': '登录',
    'login.submitting': '登录中...',
    'login.error.invalid': '密码错误',
    'login.error.network': '网络错误',

    // Header
    'header.logout': '退出登录',

    // Tabs
    'tab.status': '服务状态',
    'tab.providers': '供应商管理',
    'tab.certs': '证书信息',
    'tab.usage': '使用统计',

    // Status
    'status.running': '运行中',
    'status.stopped': '已停止',
    'status.service_status': '服务状态',
    'status.uptime': '运行时长',
    'status.total_requests': '总请求数',
    'status.active_provider': '当前供应商',
    'status.no_provider': '未配置供应商，请添加供应商。',
    'status.provider_requests_total': 'Provider 请求总数',
    'status.today_provider_requests': '今日 Provider 请求数',
    'status.today_token_consumption': '今日 Token 消耗',
    'status.usage_coverage': 'Usage 覆盖率',
    'status.last_provider_request': '最近一次 Provider 请求',

    // Providers
    'providers.title': '供应商管理',
    'providers.add': '添加供应商',
    'providers.empty': '暂无供应商，请点击"添加供应商"开始配置。',
    'providers.active': '使用中',
    'providers.edit': '编辑',
    'providers.test': '测试',
    'providers.set_active': '设为当前',
    'providers.disable': '禁用',
    'providers.enable': '启用',
    'providers.delete': '删除',
    'providers.duplicate': '复制',
    'providers.not_set': '未设置',
    'providers.confirm_delete': '确定要删除这个供应商吗？',

    // Provider Modal
    'modal.add_title': '添加供应商',
    'modal.edit_title': '编辑供应商',
    'modal.name': '供应商名称 *',
    'modal.api_url': 'API 地址 *',
    'modal.api_token': 'API Token',
    'modal.api_token_placeholder': '编辑时留空保持原值',
    'modal.supports_thinking': '支持 Thinking（扩展思考）',
    'modal.supports_thinking_hint': '后端支持 Anthropic thinking 字段时勾选（如 Kimi K2.6），否则请求中的 thinking 字段会被剥离',
    'modal.model_mappings': '模型映射',
    'modal.add_mapping': '+ 添加映射',
    'modal.cancel': '取消',
    'modal.test': '测试连接',
    'modal.save': '保存',
    'modal.required': '请填写必填字段',
    'modal.provider_created': '供应商已创建',
    'modal.provider_updated': '供应商已更新',
    'modal.save_failed': '保存失败',
    'modal.enter_api_url': '请输入 API 地址',
    'modal.connection_ok': '连接成功 (HTTP {code})',
    'modal.connection_404': '网络可达 (HTTP 404) - 根路径不支持直接访问，这是正常的',
    'modal.connection_auth': '网络可达，但认证失败 (HTTP {code}) - 请检查 API Token',
    'modal.connection_other': '收到响应 (HTTP {code}) - 请确认 API 地址是否正确',
    'modal.connection_failed': '连接失败: {error}',
    'modal.test_ok': '连接成功 (HTTP {code})',
    'modal.test_fail': '连接失败: {error}',

    // Certificates
    'certs.title': '证书信息',
    'certs.ca_path': 'CA 证书路径',
    'certs.ca_expires': 'CA 证书有效期',
    'certs.node_config': 'NODE_EXTRA_CA_CERTS 配置',
    'certs.copy': '复制',
    'certs.copied': '已复制！',

    // Usage
    'usage.overview': '概览',
    'usage.requests': '请求日志',
    'usage.providers': 'Provider',
    'usage.models': '模型',
    'usage.coverage': 'Usage 覆盖率',
    'usage.loading': '加载中...',
    'usage.refresh': '刷新',
    'usage.empty': '暂无数据',
    'usage.provider_requests_total': 'Provider 请求总数',
    'usage.token_consumption_total': 'Token 消耗总量',
    'usage.usage_coverage': 'Usage 覆盖率',
    'usage.failed_requests': '失败数',
    'usage.source_entrypoint': '来源入口',
    'usage.usage_status': 'Usage 状态',
    'usage.time_range': '时间范围',
    'usage.from': '开始',
    'usage.to': '结束',
    'usage.provider': 'Provider',
    'usage.model': '模型',
    'usage.status': '状态',
    'usage.usage_source': 'Usage 来源',
    'usage.search': '搜索',
    'usage.search_placeholder': '搜索 provider、模型、请求 ID、错误摘要',
    'usage.all': '全部',
    'usage.source_all': '全部来源',
    'usage.source_cli': 'CLI',
    'usage.source_claude_vscode': 'VS Code',
    'usage.source_unknown': '未知',
    'usage.usage_source_all': '全部来源',
    'usage.usage_source_provider': 'provider',
    'usage.usage_source_none': '无 usage',
    'usage.status_all': '全部状态',
    'usage.source_all_entrypoint': '全部入口',

    // Test results
    'test.activate_failed': '激活供应商失败',
    'test.toggle_failed': '操作失败',
  },
  en: {
    'login.subtitle': 'Enter your admin password to continue',
    'login.password': 'Password',
    'login.password_placeholder': 'Enter password',
    'login.submit': 'Login',
    'login.submitting': 'Logging in...',
    'login.error.invalid': 'Invalid password',
    'login.error.network': 'Network error',

    'header.logout': 'Logout',

    'tab.status': 'Status',
    'tab.providers': 'Providers',
    'tab.certs': 'Certificates',
    'tab.usage': 'Usage',

    'status.running': 'Running',
    'status.stopped': 'Stopped',
    'status.service_status': 'Service Status',
    'status.uptime': 'Uptime',
    'status.total_requests': 'Total Requests',
    'status.active_provider': 'Active Provider',
    'status.no_provider': 'No provider configured. Please add a provider.',
    'status.provider_requests_total': 'Provider Requests',
    'status.today_provider_requests': 'Today Provider Requests',
    'status.today_token_consumption': 'Today Token Consumption',
    'status.usage_coverage': 'Usage Coverage',
    'status.last_provider_request': 'Last Provider Request',

    'providers.title': 'Providers',
    'providers.add': 'Add Provider',
    'providers.empty': 'No providers yet. Click "Add Provider" to get started.',
    'providers.active': 'Active',
    'providers.edit': 'Edit',
    'providers.test': 'Test',
    'providers.set_active': 'Set Active',
    'providers.disable': 'Disable',
    'providers.enable': 'Enable',
    'providers.delete': 'Delete',
    'providers.duplicate': 'Duplicate',
    'providers.not_set': 'Not set',
    'providers.confirm_delete': 'Are you sure you want to delete this provider?',

    'modal.add_title': 'Add Provider',
    'modal.edit_title': 'Edit Provider',
    'modal.name': 'Provider Name *',
    'modal.api_url': 'API URL *',
    'modal.api_token': 'API Token',
    'modal.api_token_placeholder': 'Leave empty to keep current value',
    'modal.supports_thinking': 'Supports Thinking (Extended Thinking)',
    'modal.supports_thinking_hint': 'Check if the backend supports the Anthropic thinking field (e.g. Kimi K2.6). Otherwise the thinking field will be stripped.',
    'modal.model_mappings': 'Model Mappings',
    'modal.add_mapping': '+ Add Mapping',
    'modal.cancel': 'Cancel',
    'modal.test': 'Test',
    'modal.save': 'Save',
    'modal.required': 'Please fill in required fields',
    'modal.provider_created': 'Provider created',
    'modal.provider_updated': 'Provider updated',
    'modal.save_failed': 'Save failed',
    'modal.enter_api_url': 'Please enter API URL',
    'modal.connection_ok': 'Connection successful (HTTP {code})',
    'modal.connection_404': 'Network reachable (HTTP 404) - root path not accessible, this is normal',
    'modal.connection_auth': 'Network reachable, auth failed (HTTP {code}) - check API Token',
    'modal.connection_other': 'Response received (HTTP {code}) - verify API URL',
    'modal.connection_failed': 'Connection failed: {error}',
    'modal.test_ok': 'Connection successful (HTTP {code})',
    'modal.test_fail': 'Connection failed: {error}',

    'certs.title': 'Certificates',
    'certs.ca_path': 'CA Certificate Path',
    'certs.ca_expires': 'CA Certificate Expires',
    'certs.node_config': 'NODE_EXTRA_CA_CERTS Configuration',
    'certs.copy': 'Copy',
    'certs.copied': 'Copied!',

    'usage.overview': 'Overview',
    'usage.requests': 'Requests',
    'usage.providers': 'Provider',
    'usage.models': 'Models',
    'usage.coverage': 'Usage Coverage',
    'usage.loading': 'Loading...',
    'usage.refresh': 'Refresh',
    'usage.empty': 'No data yet',
    'usage.provider_requests_total': 'Provider Requests',
    'usage.token_consumption_total': 'Token Consumption',
    'usage.usage_coverage': 'Usage Coverage',
    'usage.failed_requests': 'Failed Requests',
    'usage.source_entrypoint': 'Source Entrypoint',
    'usage.usage_status': 'Usage Status',
    'usage.time_range': 'Time Range',
    'usage.from': 'From',
    'usage.to': 'To',
    'usage.provider': 'Provider',
    'usage.model': 'Model',
    'usage.status': 'Status',
    'usage.usage_source': 'Usage Source',
    'usage.search': 'Search',
    'usage.search_placeholder': 'Search provider, model, request ID, error summary',
    'usage.all': 'All',
    'usage.source_all': 'All sources',
    'usage.source_cli': 'CLI',
    'usage.source_claude_vscode': 'VS Code',
    'usage.source_unknown': 'Unknown',
    'usage.usage_source_all': 'All sources',
    'usage.usage_source_provider': 'provider',
    'usage.usage_source_none': 'No usage',
    'usage.status_all': 'All status',
    'usage.source_all_entrypoint': 'All entrypoints',

    'test.activate_failed': 'Failed to activate provider',
    'test.toggle_failed': 'Failed to toggle provider',
  },
}

export function useI18n() {
  function t(key: string, params?: Record<string, string | number>): string {
    let text = messages[locale.value][key] || key
    if (params) {
      for (const [k, v] of Object.entries(params)) {
        text = text.replace(`{${k}}`, String(v))
      }
    }
    return text
  }

  function setLocale(l: Locale) {
    locale.value = l
  }

  return { locale, t, setLocale }
}
