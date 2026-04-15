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

    // Status
    'status.running': '运行中',
    'status.stopped': '已停止',
    'status.service_status': '服务状态',
    'status.uptime': '运行时长',
    'status.total_requests': '总请求数',
    'status.active_provider': '当前供应商',
    'status.no_provider': '未配置供应商，请添加供应商。',

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

    'status.running': 'Running',
    'status.stopped': 'Stopped',
    'status.service_status': 'Service Status',
    'status.uptime': 'Uptime',
    'status.total_requests': 'Total Requests',
    'status.active_provider': 'Active Provider',
    'status.no_provider': 'No provider configured. Please add a provider.',

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
