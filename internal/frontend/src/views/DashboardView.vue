<template>
  <div class="min-h-screen bg-muted">
    <AppHeader @logout="handleLogout" />

    <div :class="['mx-auto px-6 py-8', containerClass]">
      <div class="flex flex-wrap gap-1 mb-8 bg-white p-1 rounded-lg border-2 border-border w-fit">
        <button
          v-for="tab in tabs"
          :key="tab.key"
          :class="[
            'px-6 py-2.5 rounded-md text-sm font-semibold cursor-pointer transition-all duration-150 border-none',
            activeTab === tab.key
              ? 'bg-primary text-white'
              : 'bg-transparent text-text-secondary hover:text-fg',
          ]"
          @click="activeTab = tab.key"
        >
          {{ t(tab.labelKey) }}
        </button>
      </div>

      <div v-if="activeTab === 'status'" class="space-y-6">
        <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div class="bg-primary text-white p-7 rounded-lg text-center transition-all duration-200 hover:scale-[1.02] cursor-default">
            <div class="text-[28px] font-extrabold tracking-tight">{{ status?.running ? t('status.running') : t('status.stopped') }}</div>
            <div class="text-[13px] mt-1 font-medium opacity-85">{{ t('status.service_status') }}</div>
          </div>
          <div class="bg-secondary text-white p-7 rounded-lg text-center transition-all duration-200 hover:scale-[1.02] cursor-default">
            <div class="text-[28px] font-extrabold tracking-tight">{{ status?.uptime || '-' }}</div>
            <div class="text-[13px] mt-1 font-medium opacity-85">{{ t('status.uptime') }}</div>
          </div>
          <div class="bg-accent text-white p-7 rounded-lg text-center transition-all duration-200 hover:scale-[1.02] cursor-default">
            <div class="text-[28px] font-extrabold tracking-tight">{{ formatNumber(status?.requests_total ?? status?.service_requests_total ?? 0) }}</div>
            <div class="text-[13px] mt-1 font-medium opacity-85">{{ t('status.total_requests') }}</div>
          </div>
        </div>

        <div class="grid grid-cols-2 xl:grid-cols-4 gap-4">
          <div class="bg-white p-5 rounded-lg border-2 border-border">
            <div class="text-xs font-bold text-text-secondary uppercase tracking-widest">{{ t('status.provider_requests_total') }}</div>
            <div class="mt-2 text-2xl font-bold">{{ formatNumber(status?.provider_requests_total ?? 0) }}</div>
          </div>
          <div class="bg-white p-5 rounded-lg border-2 border-border">
            <div class="text-xs font-bold text-text-secondary uppercase tracking-widest">{{ t('status.today_provider_requests') }}</div>
            <div class="mt-2 text-2xl font-bold">{{ formatNumber(status?.today_provider_requests ?? 0) }}</div>
          </div>
          <div class="bg-white p-5 rounded-lg border-2 border-border">
            <div class="text-xs font-bold text-text-secondary uppercase tracking-widest">{{ t('status.today_token_consumption') }}</div>
            <div class="mt-2 text-2xl font-bold">{{ formatNumber(status?.today_token_consumption ?? 0) }}</div>
          </div>
          <div class="bg-white p-5 rounded-lg border-2 border-border">
            <div class="flex items-center gap-1.5 text-xs font-bold text-text-secondary uppercase tracking-widest">
              <span>{{ t('status.usage_coverage') }}</span>
              <UsageCoverageHelp />
            </div>
            <div class="mt-2 text-2xl font-bold">{{ formatPercent(status?.usage_coverage ?? 0) }}</div>
          </div>
        </div>

        <div class="grid grid-cols-1 xl:grid-cols-2 gap-4">
          <div v-if="activeProvider" class="bg-white p-6 rounded-lg border-2 border-border">
            <h3 class="text-xs font-bold text-text-secondary uppercase tracking-widest mb-3.5">{{ t('status.active_provider') }}</h3>
            <div class="text-xl font-bold mb-1">{{ activeProvider.name }}</div>
            <div class="text-[13px] text-text-secondary font-mono">{{ activeProvider.api_url }}</div>
            <div v-if="Object.keys(activeProvider.model_mappings).length" class="flex flex-wrap gap-2 mt-3.5">
              <span v-for="(to, from) in activeProvider.model_mappings" :key="from" class="bg-primary-light text-primary px-3.5 py-1 rounded-full text-xs font-semibold">
                {{ from }} &rarr; {{ to }}
              </span>
            </div>
          </div>
          <div v-else class="bg-white p-6 rounded-lg border-2 border-border text-center">
            <p class="text-danger font-medium">{{ t('status.no_provider') }}</p>
          </div>

          <div class="bg-white p-6 rounded-lg border-2 border-border">
            <h3 class="text-xs font-bold text-text-secondary uppercase tracking-widest mb-3.5">{{ t('status.last_provider_request') }}</h3>
            <div class="text-lg font-semibold">{{ formatDateTime(status?.last_provider_request || null) }}</div>
            <div class="mt-2 text-sm text-text-secondary">
              {{ formatDateTime(status?.last_request || null) }}
            </div>
          </div>
        </div>
      </div>

      <div v-if="activeTab === 'providers'">
        <div class="flex items-center justify-between mb-5">
          <div class="flex items-center gap-2 text-[15px] font-bold">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <rect x="2" y="2" width="20" height="8" rx="2" /><rect x="2" y="14" width="20" height="8" rx="2" /><circle cx="6" cy="6" r="1" /><circle cx="6" cy="18" r="1" />
            </svg>
            {{ t('providers.title') }}
          </div>
          <button class="flex items-center gap-2 px-5 py-2.5 bg-primary text-white border-none rounded-lg text-sm font-semibold cursor-pointer transition-all duration-200 hover:bg-primary-hover hover:scale-[1.02]" @click="openAddModal">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
              <line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" />
            </svg>
            {{ t('providers.add') }}
          </button>
        </div>

        <div v-if="providers.length === 0" class="text-center py-12 text-text-secondary">{{ t('providers.empty') }}</div>

        <ProviderCard
          v-for="p in providers"
          :key="p.id"
          :provider="p"
          @edit="openEditModal(p)"
          @delete="handleDelete(p.id)"
          @activate="handleActivate(p.id)"
          @toggle="handleToggle(p.id)"
          @test="handleTest(p.id)"
          @duplicate="handleDuplicate"
        />
      </div>

      <div v-if="activeTab === 'certs'">
        <div class="flex items-center gap-2 text-[15px] font-bold mb-5">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <rect x="3" y="11" width="18" height="11" rx="2" /><path d="M7 11V7a5 5 0 0110 0v4" />
          </svg>
          {{ t('certs.title') }}
        </div>

        <div v-if="certs" class="space-y-3">
          <div class="p-5 bg-white rounded-lg border-2 border-border">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-1.5">{{ t('certs.ca_path') }}</label>
            <div class="text-sm font-medium">{{ certs.ca_cert_path }}</div>
          </div>
          <div class="p-5 bg-white rounded-lg border-2 border-border">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-1.5">{{ t('certs.ca_expires') }}</label>
            <div class="text-sm font-medium">{{ formatDate(certs.ca_expires_at) }}</div>
          </div>
          <div class="p-5 bg-white rounded-lg border-2 border-border">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-1.5">{{ t('certs.node_config') }}</label>
            <div class="bg-fg text-gray-200 px-4 py-3.5 rounded-lg font-mono text-[13px] flex justify-between items-center mt-2 gap-3">
              <span class="break-all">NODE_EXTRA_CA_CERTS={{ certs.ca_cert_path }}</span>
              <button class="px-3.5 py-1 bg-primary text-white border-none rounded text-xs font-semibold cursor-pointer transition-all duration-200 hover:scale-105 shrink-0" @click="copyPath">{{ t('certs.copy') }}</button>
            </div>
          </div>
        </div>
      </div>

      <div v-if="activeTab === 'usage'" class="space-y-6">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="flex flex-wrap gap-1 bg-white p-1 rounded-lg border-2 border-border">
            <button
              v-for="tab in usageTabs"
              :key="tab.key"
              :class="[
                'px-4 py-2 rounded-md text-sm font-semibold cursor-pointer transition-all duration-150 border-none',
                activeUsageTab === tab.key
                  ? 'bg-primary text-white'
                  : 'bg-transparent text-text-secondary hover:text-fg',
              ]"
              @click="activeUsageTab = tab.key"
            >
              {{ t(tab.labelKey) }}
            </button>
          </div>

          <button class="px-4 py-2 bg-white rounded-lg border-2 border-border text-sm font-semibold cursor-pointer hover:border-primary hover:text-primary" @click="loadUsageData">
            {{ t('usage.refresh') }}
          </button>
        </div>

        <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-3">
          <div class="bg-white p-4 rounded-lg border-2 border-border">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.from') }}</label>
            <input v-model="usageFilters.from" type="date" class="w-full rounded-md border border-border px-3 py-2 text-sm bg-white" />
          </div>
          <div class="bg-white p-4 rounded-lg border-2 border-border">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.to') }}</label>
            <input v-model="usageFilters.to" type="date" class="w-full rounded-md border border-border px-3 py-2 text-sm bg-white" />
          </div>
          <div class="bg-white p-4 rounded-lg border-2 border-border">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.provider') }}</label>
            <select v-model="usageFilters.provider_id" class="w-full rounded-md border border-border px-3 py-2 text-sm bg-white">
              <option value="all">{{ t('usage.all') }}</option>
              <option v-for="provider in providerOptions" :key="provider.value" :value="provider.value">{{ provider.label }}</option>
            </select>
          </div>
          <div class="bg-white p-4 rounded-lg border-2 border-border">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.model') }}</label>
            <select v-model="usageFilters.model" class="w-full rounded-md border border-border px-3 py-2 text-sm bg-white">
              <option value="all">{{ t('usage.all') }}</option>
              <option v-for="model in modelOptions" :key="model" :value="model">{{ model }}</option>
            </select>
          </div>
          <div class="bg-white p-4 rounded-lg border-2 border-border">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.status') }}</label>
            <select v-model="usageFilters.status" class="w-full rounded-md border border-border px-3 py-2 text-sm bg-white">
              <option value="all">{{ t('usage.status_all') }}</option>
              <option value="success">success</option>
              <option value="error">error</option>
            </select>
          </div>
          <div class="bg-white p-4 rounded-lg border-2 border-border">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.usage_source') }}</label>
            <select v-model="usageFilters.usage_source" class="w-full rounded-md border border-border px-3 py-2 text-sm bg-white">
              <option value="all">{{ t('usage.usage_source_all') }}</option>
              <option value="provider">{{ t('usage.usage_source_provider') }}</option>
              <option value="none">{{ t('usage.usage_source_none') }}</option>
            </select>
          </div>
          <div class="bg-white p-4 rounded-lg border-2 border-border">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.source_entrypoint') }}</label>
            <select v-model="usageFilters.source_entrypoint" class="w-full rounded-md border border-border px-3 py-2 text-sm bg-white">
              <option value="all">{{ t('usage.source_all_entrypoint') }}</option>
              <option value="cli">{{ t('usage.source_cli') }}</option>
              <option value="claude-vscode">{{ t('usage.source_claude_vscode') }}</option>
              <option value="unknown">{{ t('usage.source_unknown') }}</option>
            </select>
          </div>
          <div class="bg-white p-4 rounded-lg border-2 border-border md:col-span-2 xl:col-span-4">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.search') }}</label>
            <input v-model="usageFilters.q" type="text" :placeholder="t('usage.search_placeholder')" class="w-full rounded-md border border-border px-3 py-2 text-sm bg-white" />
          </div>
        </div>

        <div v-if="activeUsageTab === 'overview'" class="space-y-6">
          <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-4">
            <div class="bg-white p-5 rounded-lg border-2 border-border">
              <div class="text-xs font-bold text-text-secondary uppercase tracking-widest">{{ t('usage.provider_requests_total') }}</div>
              <div class="mt-2 text-2xl font-bold">{{ formatNumber(usageSummary?.provider_requests_total ?? 0) }}</div>
            </div>
            <div class="bg-white p-5 rounded-lg border-2 border-border">
              <div class="text-xs font-bold text-text-secondary uppercase tracking-widest">{{ t('usage.failed_requests') }}</div>
              <div class="mt-2 text-2xl font-bold">{{ formatNumber(usageSummary?.failed_requests ?? 0) }}</div>
            </div>
            <div class="bg-white p-5 rounded-lg border-2 border-border">
              <div class="text-xs font-bold text-text-secondary uppercase tracking-widest">{{ t('usage.token_consumption_total') }}</div>
              <div class="mt-2 text-2xl font-bold">{{ formatNumber(usageSummary?.token_consumption_total ?? 0) }}</div>
            </div>
            <div class="bg-white p-5 rounded-lg border-2 border-border">
              <div class="flex items-center gap-1.5 text-xs font-bold text-text-secondary uppercase tracking-widest">
                <span>{{ t('usage.usage_coverage') }}</span>
                <UsageCoverageHelp />
              </div>
              <div class="mt-2 text-2xl font-bold">{{ formatPercent(usageSummary?.usage_coverage ?? 0) }}</div>
            </div>
          </div>

          <div class="bg-white p-5 rounded-lg border-2 border-border">
            <div class="flex items-center justify-between mb-4">
              <div class="text-sm font-bold uppercase tracking-widest text-text-secondary">{{ t('usage.overview') }}</div>
              <div v-if="usageLoading" class="text-sm text-text-secondary">{{ t('usage.loading') }}</div>
            </div>
            <div ref="usageChartEl" class="h-[360px] w-full"></div>
          </div>
        </div>

        <div v-if="activeUsageTab === 'requests'" class="bg-white p-5 rounded-lg border-2 border-border overflow-x-auto">
          <div class="flex items-center justify-between mb-4">
            <div class="text-sm font-bold uppercase tracking-widest text-text-secondary">{{ t('usage.requests') }}</div>
            <div class="text-sm text-text-secondary">{{ formatNumber(usageRequests?.total ?? 0) }}</div>
          </div>
          <table class="min-w-[4200px] w-full text-sm">
            <thead>
              <tr class="border-b border-border text-left text-xs uppercase tracking-widest text-text-secondary">
                <th class="py-3 pr-4">id</th>
                <th class="py-3 pr-4">request_id</th>
                <th class="py-3 pr-4">started_at</th>
                <th class="py-3 pr-4">ended_at</th>
                <th class="py-3 pr-4">duration_ms</th>
                <th class="py-3 pr-4">upstream_response_header_ms</th>
                <th class="py-3 pr-4">time_to_first_byte_ms</th>
                <th class="py-3 pr-4">status_code</th>
                <th class="py-3 pr-4">error_type</th>
                <th class="py-3 pr-4">error_message</th>
                <th class="py-3 pr-4">method</th>
                <th class="py-3 pr-4">request_path</th>
                <th class="py-3 pr-4">backend_url</th>
                <th class="py-3 pr-4">provider_id</th>
                <th class="py-3 pr-4">provider_name</th>
                <th class="py-3 pr-4">provider_api_url</th>
                <th class="py-3 pr-4">source_app</th>
                <th class="py-3 pr-4">source_entrypoint</th>
                <th class="py-3 pr-4">user_agent</th>
                <th class="py-3 pr-4">original_model</th>
                <th class="py-3 pr-4">mapped_model</th>
                <th class="py-3 pr-4">stream</th>
                <th class="py-3 pr-4">request_bytes</th>
                <th class="py-3 pr-4">response_bytes</th>
                <th class="py-3 pr-4">tokens</th>
                <th class="py-3 pr-4">input_tokens</th>
                <th class="py-3 pr-4">output_tokens</th>
                <th class="py-3 pr-4">cache_creation_input_tokens</th>
                <th class="py-3 pr-4">cache_read_input_tokens</th>
                <th class="py-3 pr-4">usage_source</th>
                <th class="py-3 pr-4">usage_parse_status</th>
                <th class="py-3 pr-4">usage_parse_error</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in usageRequests?.rows || []" :key="row.id" class="border-b border-border/70">
                <td class="py-3 pr-4 font-mono text-xs">{{ formatText(row.id) }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ formatText(row.request_id) }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ formatDateTime(row.started_at) }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ formatDateTime(row.ended_at) }}</td>
                <td class="py-3 pr-4">{{ formatPlainNumber(row.duration_ms) }}</td>
                <td class="py-3 pr-4">{{ formatPlainNumber(row.upstream_response_header_ms) }}</td>
                <td class="py-3 pr-4">{{ formatPlainNumber(row.time_to_first_byte_ms) }}</td>
                <td class="py-3 pr-4">{{ row.status_code ?? '-' }}</td>
                <td class="py-3 pr-4">{{ formatText(row.error_type) }}</td>
                <td class="py-3 pr-4 break-all">{{ formatText(row.error_message) }}</td>
                <td class="py-3 pr-4">{{ formatText(row.method) }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ formatText(row.request_path) }}</td>
                <td class="py-3 pr-4 font-mono text-xs break-all">{{ formatText(row.backend_url) }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ formatText(row.provider_id) }}</td>
                <td class="py-3 pr-4">{{ formatText(row.provider_name) }}</td>
                <td class="py-3 pr-4 font-mono text-xs break-all">{{ formatText(row.provider_api_url) }}</td>
                <td class="py-3 pr-4">{{ formatText(row.source_app) }}</td>
                <td class="py-3 pr-4">{{ formatText(row.source_entrypoint) }}</td>
                <td class="py-3 pr-4 break-all">{{ formatText(row.user_agent) }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ formatText(row.original_model) }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ formatText(row.mapped_model) }}</td>
                <td class="py-3 pr-4">{{ formatBoolean(row.stream) }}</td>
                <td class="py-3 pr-4">{{ formatNumber(row.request_bytes) }}</td>
                <td class="py-3 pr-4">{{ formatNumber(row.response_bytes) }}</td>
                <td class="py-3 pr-4 font-mono text-xs font-bold">{{ formatNumber(tokenTotal(row)) }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ formatNumber(row.input_tokens) }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ formatNumber(row.output_tokens) }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ formatNumber(row.cache_creation_input_tokens) }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ formatNumber(row.cache_read_input_tokens) }}</td>
                <td class="py-3 pr-4">
                  <span :class="badgeClass(row.usage_source !== 'none')">{{ formatText(row.usage_source) }}</span>
                </td>
                <td class="py-3 pr-4">
                  <span :class="badgeClass(row.usage_parse_status === 'ok')">
                    {{ row.usage_parse_status }}
                  </span>
                </td>
                <td class="py-3 pr-4 break-all">{{ formatText(row.usage_parse_error) }}</td>
              </tr>
            </tbody>
          </table>
          <div v-if="!usageRequests?.rows?.length" class="py-10 text-center text-text-secondary">{{ t('usage.empty') }}</div>
        </div>

        <div v-if="activeUsageTab === 'providers'" class="bg-white p-5 rounded-lg border-2 border-border overflow-x-auto">
          <div class="mb-4 text-sm font-bold uppercase tracking-widest text-text-secondary">{{ t('usage.providers') }}</div>
          <table class="min-w-[1000px] w-full text-sm">
            <thead>
              <tr class="border-b border-border text-left text-xs uppercase tracking-widest text-text-secondary">
                <th class="py-3 pr-4">{{ t('usage.provider') }}</th>
                <th class="py-3 pr-4">{{ t('usage.provider_requests_total') }}</th>
                <th class="py-3 pr-4">{{ t('usage.failed_requests') }}</th>
                <th class="py-3 pr-4">{{ t('usage.token_consumption_total') }}</th>
                <th class="py-3 pr-4">
                  <span class="inline-flex items-center gap-1.5">
                    <span>{{ t('usage.usage_coverage') }}</span>
                    <UsageCoverageHelp />
                  </span>
                </th>
                <th class="py-3 pr-4">Avg ms</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in usageProviders" :key="row.name" class="border-b border-border/70">
                <td class="py-3 pr-4">{{ row.provider_name || row.name }}</td>
                <td class="py-3 pr-4">{{ formatNumber(row.total_requests) }}</td>
                <td class="py-3 pr-4">{{ formatNumber(row.failed_requests) }}</td>
                <td class="py-3 pr-4">{{ formatNumber(row.token_consumption_total) }}</td>
                <td class="py-3 pr-4">{{ formatPercent(row.usage_coverage) }}</td>
                <td class="py-3 pr-4">{{ formatNumber(row.average_duration_ms) }}</td>
              </tr>
            </tbody>
          </table>
          <div v-if="!usageProviders.length" class="py-10 text-center text-text-secondary">{{ t('usage.empty') }}</div>
        </div>

        <div v-if="activeUsageTab === 'models'" class="bg-white p-5 rounded-lg border-2 border-border overflow-x-auto">
          <div class="mb-4 text-sm font-bold uppercase tracking-widest text-text-secondary">{{ t('usage.models') }}</div>
          <table class="min-w-[1000px] w-full text-sm">
            <thead>
              <tr class="border-b border-border text-left text-xs uppercase tracking-widest text-text-secondary">
                <th class="py-3 pr-4">{{ t('usage.model') }}</th>
                <th class="py-3 pr-4">{{ t('usage.provider_requests_total') }}</th>
                <th class="py-3 pr-4">{{ t('usage.failed_requests') }}</th>
                <th class="py-3 pr-4">{{ t('usage.token_consumption_total') }}</th>
                <th class="py-3 pr-4">
                  <span class="inline-flex items-center gap-1.5">
                    <span>{{ t('usage.usage_coverage') }}</span>
                    <UsageCoverageHelp />
                  </span>
                </th>
                <th class="py-3 pr-4">Avg ms</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in usageModels" :key="row.name" class="border-b border-border/70">
                <td class="py-3 pr-4">{{ row.mapped_model || row.name }}</td>
                <td class="py-3 pr-4">{{ formatNumber(row.total_requests) }}</td>
                <td class="py-3 pr-4">{{ formatNumber(row.failed_requests) }}</td>
                <td class="py-3 pr-4">{{ formatNumber(row.token_consumption_total) }}</td>
                <td class="py-3 pr-4">{{ formatPercent(row.usage_coverage) }}</td>
                <td class="py-3 pr-4">{{ formatNumber(row.average_duration_ms) }}</td>
              </tr>
            </tbody>
          </table>
          <div v-if="!usageModels.length" class="py-10 text-center text-text-secondary">{{ t('usage.empty') }}</div>
        </div>

        <div v-if="activeUsageTab === 'coverage'" class="bg-white p-5 rounded-lg border-2 border-border overflow-x-auto">
          <div class="mb-4 text-sm font-bold uppercase tracking-widest text-text-secondary">{{ t('usage.coverage') }}</div>
          <table class="min-w-[1400px] w-full text-sm">
            <thead>
              <tr class="border-b border-border text-left text-xs uppercase tracking-widest text-text-secondary">
                <th class="py-3 pr-4">{{ t('usage.provider') }}</th>
                <th class="py-3 pr-4">API URL</th>
                <th class="py-3 pr-4">{{ t('usage.model') }}</th>
                <th class="py-3 pr-4">{{ t('usage.source_entrypoint') }}</th>
                <th class="py-3 pr-4">{{ t('usage.provider_requests_total') }}</th>
                <th class="py-3 pr-4">
                  <span class="inline-flex items-center gap-1.5">
                    <span>{{ t('usage.usage_coverage') }}</span>
                    <UsageCoverageHelp />
                  </span>
                </th>
                <th class="py-3 pr-4">{{ t('usage.usage_status') }}</th>
                <th class="py-3 pr-4">Last Seen</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in usageCoverage" :key="row.provider_name + row.provider_api_url + row.mapped_model + row.source_entrypoint" class="border-b border-border/70">
                <td class="py-3 pr-4">{{ row.provider_name }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ row.provider_api_url }}</td>
                <td class="py-3 pr-4">{{ row.mapped_model }}</td>
                <td class="py-3 pr-4">{{ formatEntrypoint(row.source_entrypoint) }}</td>
                <td class="py-3 pr-4">{{ formatNumber(row.total_requests) }}</td>
                <td class="py-3 pr-4">{{ formatPercent(row.usage_coverage) }}</td>
                <td class="py-3 pr-4">{{ row.top_usage_parse_status || '-' }}</td>
                <td class="py-3 pr-4">{{ formatDateTime(row.last_seen_at) }}</td>
              </tr>
            </tbody>
          </table>
          <div v-if="!usageCoverage.length" class="py-10 text-center text-text-secondary">{{ t('usage.empty') }}</div>
        </div>
      </div>
    </div>

    <ProviderModal v-if="showModal" :provider="editingProvider" @close="closeModal" @saved="handleSaved" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import * as echarts from 'echarts'
import {
  useApi,
  type Provider,
  type StatusInfo,
  type CertificateInfo,
  type UsageSummary,
  type UsageTrendPoint,
  type UsageRequestPage,
  type UsageRequestRow,
  type UsageAggregateRow,
  type UsageCoverageRow,
} from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'
import AppHeader from '@/components/AppHeader.vue'
import ProviderCard from '@/components/ProviderCard.vue'
import ProviderModal from '@/components/ProviderModal.vue'
import UsageCoverageHelp from '@/components/UsageCoverageHelp.vue'
import { formatPercent } from '@/utils/formatters'

const router = useRouter()
const api = useApi()
const { t, locale } = useI18n()

type MainTab = 'status' | 'providers' | 'certs' | 'usage'
type UsageTab = 'overview' | 'requests' | 'providers' | 'models' | 'coverage'

const tabs: Array<{ key: MainTab; labelKey: string }> = [
  { key: 'status', labelKey: 'tab.status' },
  { key: 'providers', labelKey: 'tab.providers' },
  { key: 'certs', labelKey: 'tab.certs' },
  { key: 'usage', labelKey: 'tab.usage' },
]

const usageTabs: Array<{ key: UsageTab; labelKey: string }> = [
  { key: 'overview', labelKey: 'usage.overview' },
  { key: 'requests', labelKey: 'usage.requests' },
  { key: 'providers', labelKey: 'usage.providers' },
  { key: 'models', labelKey: 'usage.models' },
  { key: 'coverage', labelKey: 'usage.coverage' },
]

const activeTab = ref<MainTab>('status')
const activeUsageTab = ref<UsageTab>('overview')
const containerClass = computed(() => 'max-w-[1440px]')

const status = ref<StatusInfo | null>(null)
const providers = ref<Provider[]>([])
const activeProviderId = ref('')
const certs = ref<CertificateInfo | null>(null)

const usageSummary = ref<UsageSummary | null>(null)
const usageTrends = ref<UsageTrendPoint[]>([])
const usageRequests = ref<UsageRequestPage | null>(null)
const usageProviders = ref<UsageAggregateRow[]>([])
const usageModels = ref<UsageAggregateRow[]>([])
const usageCoverage = ref<UsageCoverageRow[]>([])
const usageLoading = ref(false)
const usageChartEl = ref<HTMLDivElement | null>(null)
let usageChart: echarts.EChartsType | null = null
let statusRefreshTimer: number | null = null

const usageFilters = reactive({
  from: dateInputDaysAgo(7),
  to: dateInputToday(),
  provider_id: 'all',
  model: 'all',
  status: 'all',
  usage_source: 'all',
  source_entrypoint: 'all',
  q: '',
  tz: browserTimeZone(),
})

const activeProvider = computed(() => providers.value.find((p) => p.id === activeProviderId.value))
const providerOptions = computed(() => providers.value.map((provider) => ({ value: provider.id, label: provider.name })))
const modelOptions = computed(() => uniqueValues(usageModels.value.map((row) => row.mapped_model || row.name)))

const showModal = ref(false)
const editingProvider = ref<Provider | null>(null)

function openAddModal() {
  editingProvider.value = null
  showModal.value = true
}
function openEditModal(p: Provider) {
  editingProvider.value = p
  showModal.value = true
}
function closeModal() {
  showModal.value = false
  editingProvider.value = null
}

async function handleSaved() {
  closeModal()
  await loadProviders()
}

async function handleDelete(id: string) {
  if (!confirm(t('providers.confirm_delete'))) return
  await api.deleteProvider(id)
  await loadProviders()
}

async function handleActivate(id: string) {
  const res = await api.activateProvider(id)
  if (!res) alert(t('test.activate_failed'))
  await loadProviders()
}

async function handleToggle(id: string) {
  const res = await api.toggleProvider(id)
  if (!res.success) alert(t('test.toggle_failed'))
  await loadProviders()
}

async function handleTest(id: string) {
  const res = await api.testProvider(id)
  if (res.success) {
    const code = res.status_code ?? 0
    if (code >= 200 && code < 300) alert(t('modal.connection_ok', { code }))
    else if (code === 404) alert(t('modal.connection_404'))
    else if (code === 401 || code === 403) alert(t('modal.connection_auth', { code }))
    else alert(t('modal.connection_other', { code }))
  } else {
    alert(t('modal.connection_failed', { error: res.error ?? 'unknown' }))
  }
}

async function handleDuplicate(id: string) {
  await api.duplicateProvider(id)
  await loadProviders()
}

async function loadStatus() {
  try {
    status.value = await api.getStatus()
  } catch {
    // keep last value
  }
}

async function loadProviders() {
  try {
    const data = await api.getProviders()
    providers.value = data.providers
    activeProviderId.value = data.active_provider_id
  } catch {
    // keep last value
  }
}

async function loadCerts() {
  try {
    certs.value = await api.getCertificates()
  } catch {
    // keep last value
  }
}

async function loadUsageData() {
  if (activeTab.value !== 'usage') return
  usageLoading.value = true
  try {
    const params = { ...usageFilters }
    const [summary, trends, requests, providers, models, coverage] = await Promise.all([
      api.getUsageSummary(params),
      api.getUsageTrends(params),
      api.getUsageRequests({ ...params, page: 1, page_size: 50 }),
      api.getUsageProviders(params),
      api.getUsageModels(params),
      api.getUsageCoverage(params),
    ])
    usageSummary.value = summary
    usageTrends.value = trends
    usageRequests.value = requests
    usageProviders.value = providers
    usageModels.value = models
    usageCoverage.value = coverage
    await nextTick()
    updateUsageChart()
  } catch {
    // keep last value
  } finally {
    usageLoading.value = false
  }
}

async function handleLogout() {
  await api.logout()
  router.push('/login')
}

function formatDate(dateStr: string): string {
  try {
    const d = new Date(dateStr)
    if (isNaN(d.getTime())) return dateStr
    return d.toLocaleDateString(locale.value === 'zh' ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: 'long',
      day: 'numeric',
    })
  } catch {
    return dateStr
  }
}

function formatDateTime(value: string | null | undefined): string {
  if (!value) return '-'
  const d = new Date(value)
  if (isNaN(d.getTime())) return value
  return d.toLocaleString(locale.value === 'zh' ? 'zh-CN' : 'en-US', {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function formatNumber(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '0'
  return new Intl.NumberFormat(locale.value === 'zh' ? 'zh-CN' : 'en-US').format(Math.round(value))
}

function formatPlainNumber(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '-'
  return String(Math.round(value))
}

function formatText(value: string | null | undefined): string {
  return value ? value : '-'
}

function formatBoolean(value: boolean): string {
  return value ? 'true' : 'false'
}

function formatEntrypoint(value: string): string {
  if (!value) return '-'
  if (value === 'cli') return t('usage.source_cli')
  if (value === 'claude-vscode') return t('usage.source_claude_vscode')
  if (value === 'unknown') return t('usage.source_unknown')
  return value
}

function tokenTotal(row: UsageRequestRow): number {
  return row.input_tokens + row.output_tokens + row.cache_creation_input_tokens + row.cache_read_input_tokens
}

function badgeClass(active: boolean): string {
  return active
    ? 'inline-flex items-center rounded-full bg-primary-light px-2.5 py-1 text-[11px] font-semibold text-primary'
    : 'inline-flex items-center rounded-full bg-muted px-2.5 py-1 text-[11px] font-semibold text-text-secondary'
}

function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

function dateInputToday(): string {
  return new Date().toISOString().slice(0, 10)
}

function dateInputDaysAgo(days: number): string {
  const d = new Date()
  d.setDate(d.getDate() - days)
  return d.toISOString().slice(0, 10)
}

function uniqueValues(values: string[]): string[] {
  return Array.from(new Set(values.filter(Boolean))).sort((a, b) => a.localeCompare(b))
}

function updateUsageChart() {
  if (activeTab.value !== 'usage' || activeUsageTab.value !== 'overview') {
    disposeUsageChart()
    return
  }
  if (!usageChartEl.value) return
  if (!usageChart) {
    usageChart = echarts.init(usageChartEl.value)
  } else if (usageChart.getDom() !== usageChartEl.value) {
    disposeUsageChart()
    usageChart = echarts.init(usageChartEl.value)
  }

  const data = usageTrends.value
  if (!data.length) {
    usageChart.clear()
    usageChart.setOption({
      xAxis: { type: 'category', data: [] },
      yAxis: { type: 'value' },
      series: [],
      graphic: {
        type: 'text',
        left: 'center',
        top: 'middle',
        style: {
          text: t('usage.empty'),
          fill: '#6B7280',
          fontSize: 14,
        },
      },
    })
    return
  }

  usageChart.setOption({
    tooltip: { trigger: 'axis' },
    legend: { top: 0 },
    grid: { left: 48, right: 56, top: 48, bottom: 32 },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: data.map((row) => row.bucket),
    },
    yAxis: [
      { type: 'value', name: t('usage.provider_requests_total') },
      { type: 'value', name: t('usage.usage_coverage'), min: 0, max: 1, axisLabel: { formatter: (v: number) => `${Math.round(v * 100)}%` } },
    ],
    series: [
      { name: t('usage.provider_requests_total'), type: 'line', smooth: true, data: data.map((row) => row.provider_requests_total) },
      { name: t('usage.failed_requests'), type: 'line', smooth: true, data: data.map((row) => row.failed_requests) },
      { name: 'Input', type: 'line', smooth: true, data: data.map((row) => row.input_tokens) },
      { name: 'Output', type: 'line', smooth: true, data: data.map((row) => row.output_tokens) },
      { name: 'Cache Create', type: 'line', smooth: true, data: data.map((row) => row.cache_creation_input_tokens) },
      { name: 'Cache Read', type: 'line', smooth: true, data: data.map((row) => row.cache_read_input_tokens) },
      { name: t('usage.usage_coverage'), type: 'line', yAxisIndex: 1, smooth: true, data: data.map((row) => row.usage_coverage) },
    ],
  })
}

function disposeUsageChart() {
  if (usageChart) {
    usageChart.dispose()
    usageChart = null
  }
}

let filterTimer: number | null = null
function scheduleUsageLoad() {
  if (activeTab.value !== 'usage') return
  if (filterTimer) window.clearTimeout(filterTimer)
  filterTimer = window.setTimeout(() => {
    void loadUsageData()
  }, 250)
}

watch(
  () => activeTab.value,
  (tab) => {
    if (tab === 'usage') {
      void loadUsageData()
    } else {
      disposeUsageChart()
    }
  }
)

watch(
  () => activeUsageTab.value,
  async () => {
    await nextTick()
    updateUsageChart()
  }
)

watch(
  usageFilters,
  () => {
    scheduleUsageLoad()
  },
  { deep: true }
)

onMounted(async () => {
  await Promise.all([loadStatus(), loadProviders(), loadCerts()])
  void loadUsageData()
  statusRefreshTimer = window.setInterval(() => {
    void loadStatus()
  }, 30000)
  window.addEventListener('resize', updateUsageChart)
})

onBeforeUnmount(() => {
  if (statusRefreshTimer) window.clearInterval(statusRefreshTimer)
  if (filterTimer) window.clearTimeout(filterTimer)
  window.removeEventListener('resize', updateUsageChart)
  disposeUsageChart()
})
</script>
