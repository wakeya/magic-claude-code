<template>
  <div class="app-shell min-h-screen">
    <AppHeader @logout="handleLogout" @show-connection-mode="activeTab = 'connection'" />

    <div :class="['mx-auto px-4 py-6 sm:px-6 sm:py-8', containerClass]">
      <div class="app-panel flex flex-wrap gap-1 mb-8 p-1 rounded-lg w-fit">
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
          <div class="bg-accent text-white p-7 rounded-lg text-center transition-all duration-200 hover:scale-[1.02] cursor-default group relative">
            <div class="text-[28px] font-extrabold tracking-tight">{{ formatNumber(status?.requests_total ?? status?.service_requests_total ?? 0) }}</div>
            <div class="text-[13px] mt-1 font-medium opacity-85 inline-flex items-center gap-1">
              {{ t('status.total_requests') }}
              <span class="inline-flex items-center justify-center w-4 h-4 rounded-full border border-white/50 text-[10px] cursor-help opacity-70 hover:opacity-100">?</span>
            </div>
            <div class="absolute bottom-full left-1/2 -translate-x-1/2 mb-2 px-3 py-2 rounded-md bg-gray-700 text-white text-[12px] leading-snug whitespace-normal w-56 opacity-0 group-hover:opacity-100 transition-opacity pointer-events-none z-10 shadow-lg">{{ t('status.total_requests_tip') }}</div>
          </div>
        </div>

        <div class="grid grid-cols-2 xl:grid-cols-4 gap-4">
          <div class="app-panel p-5 rounded-lg group relative">
            <div class="text-xs font-bold text-text-secondary uppercase tracking-widest inline-flex items-center gap-1">
              {{ t('status.provider_requests_total') }}
              <span class="inline-flex items-center justify-center w-4 h-4 rounded-full border border-gray-300 text-[10px] cursor-help opacity-70 hover:opacity-100">?</span>
            </div>
            <div class="mt-2 text-2xl font-bold">{{ formatNumber(status?.provider_requests_total ?? 0) }}</div>
            <div class="absolute bottom-full left-1/2 -translate-x-1/2 mb-2 px-3 py-2 rounded-md bg-gray-700 text-white text-[12px] leading-snug whitespace-normal w-56 opacity-0 group-hover:opacity-100 transition-opacity pointer-events-none z-10 shadow-lg">{{ t('status.provider_requests_total_tip') }}</div>
          </div>
          <div class="app-panel p-5 rounded-lg group relative">
            <div class="text-xs font-bold text-text-secondary uppercase tracking-widest inline-flex items-center gap-1">
              {{ t('status.today_provider_requests') }}
              <span class="inline-flex items-center justify-center w-4 h-4 rounded-full border border-gray-300 text-[10px] cursor-help opacity-70 hover:opacity-100">?</span>
            </div>
            <div class="mt-2 text-2xl font-bold">{{ formatNumber(status?.today_provider_requests ?? 0) }}</div>
            <div class="absolute bottom-full left-1/2 -translate-x-1/2 mb-2 px-3 py-2 rounded-md bg-gray-700 text-white text-[12px] leading-snug whitespace-normal w-56 opacity-0 group-hover:opacity-100 transition-opacity pointer-events-none z-10 shadow-lg">{{ t('status.today_provider_requests_tip') }}</div>
          </div>
          <div class="app-panel p-5 rounded-lg">
            <div class="text-xs font-bold text-text-secondary uppercase tracking-widest">{{ t('status.today_token_consumption') }}</div>
            <div class="mt-2 text-2xl font-bold">{{ formatNumber(status?.today_token_consumption ?? 0) }}</div>
          </div>
          <div class="app-panel p-5 rounded-lg group relative">
            <div class="flex items-center gap-1.5 text-xs font-bold text-text-secondary uppercase tracking-widest">
              <span>{{ t('status.usage_coverage') }}</span>
              <UsageCoverageHelp />
            </div>
            <div class="mt-2 text-2xl font-bold">{{ formatPercent(status?.usage_coverage ?? 0) }}</div>
          </div>
        </div>

        <div class="grid grid-cols-1 xl:grid-cols-2 gap-4">
          <div v-if="activeProvider" class="app-panel p-6 rounded-lg">
            <h3 class="text-xs font-bold text-text-secondary uppercase tracking-widest mb-3.5">{{ t('status.active_provider') }}</h3>
            <div class="text-xl font-bold mb-1">{{ activeProvider.name }}</div>
            <div class="text-[13px] text-text-secondary font-mono">{{ activeProvider.api_url }}</div>
            <div v-if="Object.keys(activeProvider.model_mappings).length" class="flex flex-wrap gap-2 mt-3.5">
              <span v-for="(to, from) in activeProvider.model_mappings" :key="from" class="bg-primary-light text-primary px-3.5 py-1 rounded-full text-xs font-semibold">
                {{ from }} &rarr; {{ to }}
              </span>
            </div>
          </div>
          <div v-else class="app-panel p-6 rounded-lg text-center">
            <p class="text-danger font-medium">{{ t('status.no_provider') }}</p>
          </div>

          <div class="app-panel p-6 rounded-lg">
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

      <div v-if="activeTab === 'connection'" class="space-y-6">
        <div class="flex items-center gap-2 text-[15px] font-bold">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
            <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
          </svg>
          {{ t('tab.connection') }}
        </div>

        <div class="app-panel p-5 rounded-lg">
          <div class="flex flex-wrap items-center gap-3 text-sm">
            <span class="app-muted">{{ t('mode.current_mode') }}:</span>
            <span class="px-2.5 py-0.5 rounded-full text-xs font-bold" :style="{ background: 'var(--app-accent)', color: 'white' }">{{ modeTitle(configuredMode) }}</span>
            <template v-if="effectiveMode && effectiveMode !== configuredMode">
              <span class="app-muted">{{ t('mode.effective_mode') }}:</span>
              <span class="px-2.5 py-0.5 rounded-full text-xs font-bold bg-muted">{{ modeTitle(effectiveMode) }}</span>
            </template>
          </div>
          <p v-if="modeRationale" class="mt-2 text-xs app-muted leading-snug">{{ modeRationale }}</p>
          <div class="mt-3 inline-block px-3 py-1.5 rounded-lg text-xs font-semibold" style="background: var(--app-accent-soft); color: var(--app-accent);">
            {{ t('mode.priority_label') }}: {{ t('mode.priority') }}
          </div>
        </div>

        <div class="app-panel p-5 rounded-lg">
          <p class="text-sm app-muted mb-4">{{ t('mode.tab_subtitle') }}</p>
          <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
            <button
              v-for="opt in modeOptions"
              :key="opt.value"
              type="button"
              :class="[
                'px-4 py-3 rounded-lg text-left border transition-all duration-200 cursor-pointer',
                viewMode === opt.value
                  ? 'text-white'
                  : 'app-control',
              ]"
              :style="viewMode === opt.value ? 'background: var(--app-accent); border-color: var(--app-accent);' : ''"
              @click="viewMode = opt.value"
            >
              <div class="text-[14px] font-bold">{{ opt.label() }}</div>
              <div class="text-[11px] mt-0.5" :class="viewMode === opt.value ? 'opacity-80' : 'opacity-60'">{{ opt.hint() }}</div>
            </button>
          </div>
          <div class="mt-4 flex flex-wrap items-center gap-3">
            <button
              type="button"
              class="px-4 py-2 rounded-lg text-sm font-semibold text-white cursor-pointer transition-all duration-200 disabled:opacity-50 disabled:cursor-not-allowed"
              style="background: var(--app-accent);"
              :disabled="modeSaving || configuredMode === viewMode"
              @click="saveMode(viewMode)"
            >
              {{ t('mode.save_as_preferred') }}
            </button>
            <p v-if="modeMessage" class="text-xs font-semibold" :style="{ color: modeMessageIsError ? 'var(--app-danger)' : 'var(--app-success)' }">
              {{ modeMessage }}
            </p>
          </div>
        </div>

        <div class="app-panel p-6 rounded-lg space-y-5">
          <div class="flex items-center gap-3">
            <h3 class="text-lg font-bold">{{ modeTitle(viewMode) }}</h3>
            <span class="text-[11px] app-muted font-mono">{{ t(`mode.${viewMode}.entry`) }}</span>
          </div>
          <p class="text-sm leading-relaxed">{{ t(`mode.${viewMode}.long_desc`) }}</p>

          <div v-if="viewMode === 'gateway'" class="rounded-lg p-4" style="background: var(--app-accent-soft);">
            <h4 class="text-sm font-bold mb-1" style="color: var(--app-accent);">{{ t('mode.gateway.basic_settings') }}</h4>
            <p class="text-xs app-muted mb-4">{{ t('mode.gateway.basic_settings_desc') }}</p>
            <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('mode.gateway.listen_addr') }}</label>
                <input v-model="gatewayAddrInput" type="text" class="app-control w-full rounded-md px-3 py-2 text-sm font-mono" :placeholder="gatewayAddr" />
                <p class="mt-1 text-[11px] app-muted">{{ t('mode.gateway.listen_addr_hint') }}</p>
              </div>
              <div>
                <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('mode.gateway.listen_port') }}</label>
                <input v-model.number="gatewayPortInput" type="number" min="1024" max="65535" class="app-control w-full rounded-md px-3 py-2 text-sm font-mono" :placeholder="gatewayPort" />
                <p class="mt-1 text-[11px] app-muted">{{ t('mode.gateway.listen_port_hint') }}</p>
              </div>
            </div>
            <div class="mt-4 flex flex-wrap items-center gap-3">
              <button
                type="button"
                class="px-4 py-2 rounded-lg text-sm font-semibold text-white cursor-pointer transition-all duration-200 disabled:opacity-50 disabled:cursor-not-allowed"
                style="background: var(--app-accent);"
                :disabled="gatewaySaving"
                @click="saveGatewaySettings"
              >
                {{ t('mode.gateway.save_settings') }}
              </button>
              <p v-if="gatewayMessage" class="text-xs font-semibold" :style="{ color: gatewayMessageIsError ? 'var(--app-danger)' : 'var(--app-success)' }">
                {{ gatewayMessage }}
              </p>
            </div>
          </div>

          <div v-if="viewMode === 'gateway'" class="rounded-lg p-4" style="background: color-mix(in srgb, var(--app-danger) 8%, transparent);">
            <h4 class="text-sm font-bold mb-2" style="color: var(--app-danger);">{{ t('mode.limitations_title') }}</h4>
            <ul class="space-y-1.5 text-sm app-muted">
              <li v-for="(item, i) in t('mode.gateway.limitations').split('\n')" :key="'lim' + i" class="flex gap-2">
                <span class="shrink-0" style="color: var(--app-danger);">⚠</span>
                <span>{{ item }}</span>
              </li>
            </ul>
          </div>

          <div v-if="viewMode === 'transparent' || viewMode === 'tunnel'" class="rounded-lg p-4" style="background: color-mix(in srgb, var(--app-success) 8%, transparent);">
            <h4 class="text-sm font-bold mb-2" style="color: var(--app-success);">{{ t('mode.advantages_title') }}</h4>
            <ul class="space-y-1.5 text-sm app-muted">
              <li v-for="(item, i) in t(`mode.${viewMode}.advantages`).split('\n')" :key="'adv' + i" class="flex gap-2">
                <span class="shrink-0" style="color: var(--app-success);">✓</span>
                <span>{{ item }}</span>
              </li>
            </ul>
          </div>

          <div>
            <h4 class="text-sm font-bold mb-2">{{ t('mode.how_it_works_title') }}</h4>
            <ol class="space-y-1.5 text-sm app-muted">
              <li v-for="(step, i) in t(`mode.${viewMode}.how_it_works`).split('\n')" :key="'hw' + i" class="flex gap-2">
                <span class="font-bold shrink-0" style="color: var(--app-accent);">{{ i + 1 }}.</span>
                <span>{{ step }}</span>
              </li>
            </ol>
          </div>

          <div>
            <h4 class="text-sm font-bold mb-2">{{ t('mode.prerequisites_title') }}</h4>
            <ul class="space-y-1 text-sm app-muted">
              <li v-for="(req, i) in t(`mode.${viewMode}.prerequisites`).split('\n')" :key="'pr' + i" class="flex gap-2">
                <span class="shrink-0">•</span>
                <span>{{ req }}</span>
              </li>
            </ul>
          </div>

          <div>
            <h4 class="text-sm font-bold mb-2">{{ t('mode.steps_title') }}</h4>
            <ol class="space-y-1.5 text-sm app-muted">
              <li v-for="(step, i) in t(`mode.${viewMode}.steps`).split('\n')" :key="'st' + i" class="flex gap-2">
                <span class="font-bold shrink-0" style="color: var(--app-accent);">{{ i + 1 }}.</span>
                <span>{{ step }}</span>
              </li>
            </ol>
          </div>

          <div>
            <div class="flex items-center justify-between mb-2">
              <div>
                <h4 class="text-sm font-bold">{{ t('mode.settings_json_title') }}</h4>
                <p class="text-[11px] app-muted mt-0.5">{{ t('mode.config_path_windows') }}</p>
              </div>
              <button type="button" class="app-control px-3 py-1 rounded text-xs font-semibold cursor-pointer transition-all duration-200" @click="copySettings">
                {{ copiedSettings ? t('mode.copied') : t('mode.copy_settings') }}
              </button>
            </div>
            <pre class="app-control rounded-lg p-4 text-[12px] font-mono whitespace-pre-wrap overflow-x-auto">{{ viewMode === 'gateway' ? gatewaySettingsJson : t(`mode.${viewMode}.settings_json`) }}</pre>
          </div>

          <div class="px-3 py-2 rounded-lg text-xs font-semibold" style="background: var(--app-accent-soft); color: var(--app-accent);">
            {{ t(`mode.${viewMode}.privilege`) }}
          </div>

          <!-- 客户端推荐配置（可选增强） -->
          <details class="rounded-lg p-4" style="background: color-mix(in srgb, var(--app-accent) 6%, transparent);">
            <summary class="text-sm font-bold cursor-pointer" style="color: var(--app-accent);">
              {{ t('mode.recommended_config_title') }}
            </summary>
            <div class="mt-3 space-y-3">
              <p class="text-xs app-muted">{{ t('mode.recommended_config_desc') }}</p>

              <div class="text-xs app-muted space-y-2">
                <p class="font-bold">{{ t('mode.deps_title') }}</p>
                <div>
                  <div>{{ t('mode.deps_rtk') }}</div>
                  <pre class="app-control rounded p-2 mt-1 font-mono text-[11px] overflow-x-auto">{{ t('mode.deps_rtk_cmd') }}</pre>
                </div>
                <div>
                  <div>{{ t('mode.deps_bun') }}</div>
                  <pre class="app-control rounded p-2 mt-1 font-mono text-[11px] overflow-x-auto">{{ t('mode.deps_bun_cmd') }}</pre>
                </div>
                <div>
                  <div>{{ t('mode.deps_marketplaces') }}</div>
                  <p class="mt-1">{{ t('mode.deps_marketplaces_desc') }}</p>
                </div>
                <p class="italic">{{ t('mode.deps_statusline_note') }}</p>
              </div>

              <div class="flex items-center justify-between flex-wrap gap-2">
                <div class="text-xs font-bold">
                  <div>~/.claude/settings.json</div>
                  <div class="app-muted font-normal">{{ t('mode.config_path_windows') }}</div>
                </div>
                <button type="button" class="app-control px-3 py-1 rounded text-xs font-semibold cursor-pointer transition-all duration-200" @click="copyRecommended">
                  {{ copiedRecommended ? t('mode.copied') : t('mode.copy_recommended') }}
                </button>
              </div>
              <p class="text-[11px] app-muted italic">{{ t('mode.config_path_note') }}</p>
              <pre class="app-control rounded-lg p-4 text-[12px] font-mono whitespace-pre-wrap overflow-x-auto">{{ recommendedConfigJson }}</pre>
            </div>
          </details>
        </div>

        <p class="text-xs app-muted">{{ t('mode.footer') }}</p>
      </div>

      <div v-if="activeTab === 'certs'">
        <div class="flex items-center gap-2 text-[15px] font-bold mb-5">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <rect x="3" y="11" width="18" height="11" rx="2" /><path d="M7 11V7a5 5 0 0110 0v4" />
          </svg>
          {{ t('certs.title') }}
        </div>

        <div v-if="certs" class="space-y-3">
          <div class="app-panel p-5 rounded-lg">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-1.5">{{ t('certs.ca_path') }}</label>
            <div class="text-sm font-medium">{{ certs.ca_cert_path }}</div>
          </div>
          <div class="app-panel p-5 rounded-lg">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-1.5">{{ t('certs.ca_expires') }}</label>
            <div class="text-sm font-medium">{{ formatDate(certs.ca_expires_at) }}</div>
          </div>
          <div class="app-panel p-5 rounded-lg">
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
          <div class="app-panel flex flex-wrap gap-1 p-1 rounded-lg">
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

          <div class="flex flex-wrap items-center gap-2">
            <button class="app-control px-4 py-2 rounded-lg text-sm font-semibold cursor-pointer text-danger" @click="openUsageClearModal">
              {{ t('usage.clear_data') }}
            </button>
            <button class="app-control px-4 py-2 rounded-lg text-sm font-semibold cursor-pointer" @click="loadUsageData">
              {{ t('usage.refresh') }}
            </button>
          </div>
        </div>

        <div class="app-panel flex flex-wrap items-center gap-2 px-3 py-2 rounded-lg">
          <span class="text-xs font-bold text-text-secondary uppercase tracking-widest whitespace-nowrap">{{ t('usage.date_range') }}</span>
          <div class="flex flex-wrap gap-1">
            <button
              v-for="preset in usageDateRangePresets"
              :key="preset.key"
              :class="[
                'px-3 py-1.5 rounded-md text-sm font-semibold cursor-pointer transition-all duration-150 border-none',
                activeUsageDateRangePreset === preset.key
                  ? 'bg-primary text-white'
                  : 'bg-transparent text-text-secondary hover:text-fg',
              ]"
              @click="applyUsageDateRangePreset(preset.key)"
            >
              {{ t(preset.labelKey) }}
            </button>
          </div>
        </div>

        <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-3">
          <div class="app-panel p-4 rounded-lg">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.from') }}</label>
            <input v-model="usageFilters.from" type="datetime-local" step="1" class="app-control w-full rounded-md px-3 py-2 text-sm" />
          </div>
          <div class="app-panel p-4 rounded-lg">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.to') }}</label>
            <input v-model="usageFilters.to" type="datetime-local" step="1" class="app-control w-full rounded-md px-3 py-2 text-sm" />
          </div>
          <div class="app-panel p-4 rounded-lg">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.provider') }}</label>
            <select v-model="usageFilters.provider_id" class="app-control w-full rounded-md px-3 py-2 text-sm">
              <option value="all">{{ t('usage.all') }}</option>
              <option v-for="provider in providerOptions" :key="provider.value" :value="provider.value">{{ provider.label }}</option>
            </select>
          </div>
          <div class="app-panel p-4 rounded-lg">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.model') }}</label>
            <select v-model="usageFilters.model" class="app-control w-full rounded-md px-3 py-2 text-sm">
              <option value="all">{{ t('usage.all') }}</option>
              <option v-for="model in modelOptions" :key="model" :value="model">{{ model }}</option>
            </select>
          </div>
          <div class="app-panel p-4 rounded-lg">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.status') }}</label>
            <select v-model="usageFilters.status" class="app-control w-full rounded-md px-3 py-2 text-sm">
              <option value="all">{{ t('usage.status_all') }}</option>
              <option value="success">success</option>
              <option value="error">error</option>
            </select>
          </div>
          <div class="app-panel p-4 rounded-lg">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.usage_source') }}</label>
            <select v-model="usageFilters.usage_source" class="app-control w-full rounded-md px-3 py-2 text-sm">
              <option value="all">{{ t('usage.usage_source_all') }}</option>
              <option value="provider">{{ t('usage.usage_source_provider') }}</option>
              <option value="session_log">{{ t('usage.usage_source_session_log') }}</option>
              <option value="none">{{ t('usage.usage_source_none') }}</option>
            </select>
          </div>
          <div class="app-panel p-4 rounded-lg">
            <label class="relative mb-2 flex items-center gap-1.5 text-xs font-bold text-text-secondary uppercase tracking-widest group">
              <span>{{ t('usage.stats_scope') }}</span>
              <UsageStatsScopeHelp />
            </label>
            <select v-model="usageFilters.stats_scope" class="app-control w-full rounded-md px-3 py-2 text-sm">
              <option value="effective">{{ t('usage.stats_scope_effective') }}</option>
              <option value="provider">{{ t('usage.stats_scope_provider') }}</option>
              <option value="session_log">{{ t('usage.stats_scope_session_log') }}</option>
              <option value="raw">{{ t('usage.stats_scope_raw') }}</option>
            </select>
          </div>
          <div class="app-panel p-4 rounded-lg">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.source_entrypoint') }}</label>
            <select v-model="usageFilters.source_entrypoint" class="app-control w-full rounded-md px-3 py-2 text-sm">
              <option value="all">{{ t('usage.source_all_entrypoint') }}</option>
              <option value="cli">{{ t('usage.source_cli') }}</option>
              <option value="claude-vscode">{{ t('usage.source_claude_vscode') }}</option>
              <option value="unknown">{{ t('usage.source_unknown') }}</option>
            </select>
          </div>
          <div class="app-panel p-4 rounded-lg md:col-span-2 xl:col-span-4">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-2">{{ t('usage.search') }}</label>
            <input v-model="usageFilters.q" type="text" :placeholder="t('usage.search_placeholder')" class="app-control w-full rounded-md px-3 py-2 text-sm" />
          </div>
        </div>

        <div v-if="activeUsageTab === 'overview'" class="space-y-6">
          <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-4">
            <div class="app-panel p-5 rounded-lg">
              <div class="text-xs font-bold text-text-secondary uppercase tracking-widest">{{ t('usage.provider_requests_total') }}</div>
              <div class="mt-2 text-2xl font-bold">{{ formatNumber(usageSummary?.provider_requests_total ?? 0) }}</div>
            </div>
            <div class="app-panel p-5 rounded-lg">
              <div class="text-xs font-bold text-text-secondary uppercase tracking-widest">{{ t('usage.failed_requests') }}</div>
              <div class="mt-2 text-2xl font-bold">{{ formatNumber(usageSummary?.failed_requests ?? 0) }}</div>
            </div>
            <div class="app-panel p-5 rounded-lg">
              <div class="text-xs font-bold text-text-secondary uppercase tracking-widest">{{ t('usage.token_consumption_total') }}</div>
              <div class="mt-2 text-2xl font-bold">{{ formatNumber(usageSummary?.token_consumption_total ?? 0) }}</div>
            </div>
            <div class="app-panel p-5 rounded-lg group relative">
              <div class="flex items-center gap-1.5 text-xs font-bold text-text-secondary uppercase tracking-widest">
                <span>{{ t('usage.usage_coverage') }}</span>
                <UsageCoverageHelp />
              </div>
              <div class="mt-2 text-2xl font-bold">{{ formatPercent(usageSummary?.usage_coverage ?? 0) }}</div>
            </div>
          </div>

          <div class="app-panel p-5 rounded-lg">
            <div class="flex items-center justify-between mb-4">
              <div class="text-sm font-bold uppercase tracking-widest text-text-secondary">{{ t('usage.overview') }}</div>
              <div v-if="usageLoading" class="text-sm text-text-secondary">{{ t('usage.loading') }}</div>
            </div>
            <div ref="usageChartEl" class="h-[360px] w-full"></div>
          </div>
        </div>

        <div v-if="activeUsageTab === 'requests'" class="app-panel p-5 rounded-lg overflow-x-auto">
          <div class="flex items-center justify-between mb-4">
            <div class="text-sm font-bold uppercase tracking-widest text-text-secondary">{{ t('usage.requests') }}</div>
          </div>
          <table class="min-w-[1900px] w-full text-sm">
            <thead>
              <tr class="border-b border-border text-left text-xs uppercase tracking-widest text-text-secondary">
                <th class="py-3 pr-4">{{ t('usage.time_range') }}</th>
                <th class="py-3 pr-4">{{ t('usage.provider') }}</th>
                <th class="py-3 pr-4">{{ t('usage.model') }}</th>
                <th class="py-3 pr-4">{{ t('usage.source_entrypoint') }}</th>
                <th class="py-3 pr-4">{{ t('usage.usage_source') }}</th>
                <th class="py-3 pr-4">{{ t('usage.usage_status') }}</th>
                <th class="py-3 pr-4">{{ t('usage.duration_ms') }}</th>
                <th class="py-3 pr-4">{{ t('usage.upstream_response_header_ms') }}</th>
                <th class="py-3 pr-4">{{ t('usage.time_to_first_byte_ms') }}</th>
                <th class="py-3 pr-4">{{ t('usage.status') }}</th>
                <th class="py-3 pr-4">{{ t('usage.tokens') }}</th>
                <th class="py-3 pr-4">{{ t('usage.input_tokens') }}</th>
                <th class="py-3 pr-4">{{ t('usage.output_tokens') }}</th>
                <th class="py-3 pr-4">{{ t('usage.cache_creation_input_tokens') }}</th>
                <th class="py-3 pr-4">{{ t('usage.cache_read_input_tokens') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in usageRequests?.rows || []" :key="row.id" class="border-b border-border/70">
                <td class="py-3 pr-4 font-mono text-xs">{{ formatDateTime(row.started_at) }}</td>
                <td class="py-3 pr-4">{{ row.provider_name }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ row.mapped_model || row.original_model }}</td>
                <td class="py-3 pr-4">{{ formatEntrypoint(row.source_entrypoint) }}</td>
                <td class="py-3 pr-4">
                  <span :class="badgeClass(row.usage_source !== 'none')">{{ formatUsageSource(row.usage_source) }}</span>
                  <span v-if="row.dedupe_status === 'duplicate'" class="ml-1 inline-flex items-center rounded-full bg-muted px-2.5 py-1 text-[11px] font-semibold text-text-secondary">
                    {{ t('usage.dedupe_duplicate') }}
                  </span>
                </td>
                <td class="py-3 pr-4">
                  <span :class="badgeClass(row.usage_parse_status === 'ok')">
                    {{ row.usage_parse_status }}
                  </span>
                </td>
                <td class="py-3 pr-4">{{ formatDuration(row.duration_ms) }}</td>
                <td class="py-3 pr-4">{{ formatDuration(row.upstream_response_header_ms) }}</td>
                <td class="py-3 pr-4">{{ formatDuration(row.time_to_first_byte_ms) }}</td>
                <td class="py-3 pr-4">{{ row.status_code ?? '-' }}</td>
                <td class="py-3 pr-4 font-mono text-xs font-bold">{{ formatNumber(tokenTotal(row)) }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ formatNumber(row.input_tokens) }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ formatNumber(row.output_tokens) }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ formatNumber(row.cache_creation_input_tokens) }}</td>
                <td class="py-3 pr-4 font-mono text-xs">{{ formatNumber(row.cache_read_input_tokens) }}</td>
              </tr>
            </tbody>
          </table>
          <div v-if="!usageRequests?.rows?.length" class="py-10 text-center text-text-secondary">{{ t('usage.empty') }}</div>
          <div v-if="usageRequests?.total" class="mt-4 flex flex-wrap items-center justify-end gap-3 border-t border-border pt-4 text-sm">
            <span class="text-text-secondary">{{ t('usage.total_count', { count: usageRequests.total }) }}</span>
            <div class="flex items-center gap-2 text-text-secondary">
              <span>{{ t('usage.page_size') }}</span>
              <select v-model.number="usageRequestPageSize" class="app-control rounded-md px-2 py-1 text-sm">
                <option v-for="size in usageRequestPageSizes" :key="size" :value="size">{{ size }}</option>
              </select>
              <span>{{ t('usage.per_page') }}</span>
            </div>
            <div class="flex items-center gap-1">
              <button
                class="rounded-md border border-border px-2 py-1 text-sm font-semibold disabled:cursor-not-allowed disabled:opacity-45"
                :disabled="usageRequestPage <= 1"
                :aria-label="t('usage.first_page')"
                :title="t('usage.first_page')"
                @click="goToUsageRequestPage(1)"
              >
                &lt;&lt;
              </button>
              <button
                class="rounded-md border border-border px-2 py-1 text-sm font-semibold disabled:cursor-not-allowed disabled:opacity-45"
                :disabled="usageRequestPage <= 1"
                :aria-label="t('usage.prev_page')"
                :title="t('usage.prev_page')"
                @click="goToUsageRequestPage(usageRequestPage - 1)"
              >
                &lt;
              </button>
              <span class="px-2 text-text-secondary">{{ t('usage.page_summary', { page: usageRequestPage, total: usageRequestTotalPages }) }}</span>
              <button
                class="rounded-md border border-border px-2 py-1 text-sm font-semibold disabled:cursor-not-allowed disabled:opacity-45"
                :disabled="usageRequestPage >= usageRequestTotalPages"
                :aria-label="t('usage.next_page')"
                :title="t('usage.next_page')"
                @click="goToUsageRequestPage(usageRequestPage + 1)"
              >
                &gt;
              </button>
              <button
                class="rounded-md border border-border px-2 py-1 text-sm font-semibold disabled:cursor-not-allowed disabled:opacity-45"
                :disabled="usageRequestPage >= usageRequestTotalPages"
                :aria-label="t('usage.last_page')"
                :title="t('usage.last_page')"
                @click="goToUsageRequestPage(usageRequestTotalPages)"
              >
                &gt;&gt;
              </button>
            </div>
          </div>
        </div>

        <div v-if="activeUsageTab === 'providers'" class="app-panel p-5 rounded-lg" style="overflow-x:clip;overflow-y:visible">
          <div class="mb-4 text-sm font-bold uppercase tracking-widest text-text-secondary">{{ t('usage.providers') }}</div>
          <table class="min-w-[1000px] w-full text-sm">
            <thead>
              <tr class="border-b border-border text-left text-xs uppercase tracking-widest text-text-secondary">
                <th class="py-3 pr-4">{{ t('usage.provider') }}</th>
                <th class="py-3 pr-4">{{ t('usage.provider_requests_total') }}</th>
                <th class="py-3 pr-4">{{ t('usage.failed_requests') }}</th>
                <th class="py-3 pr-4">{{ t('usage.token_consumption_total') }}</th>
                <th class="py-3 pr-4 group relative">
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

        <div v-if="activeUsageTab === 'models'" class="app-panel p-5 rounded-lg" style="overflow-x:clip;overflow-y:visible">
          <div class="mb-4 text-sm font-bold uppercase tracking-widest text-text-secondary">{{ t('usage.models') }}</div>
          <table class="min-w-[1000px] w-full text-sm">
            <thead>
              <tr class="border-b border-border text-left text-xs uppercase tracking-widest text-text-secondary">
                <th class="py-3 pr-4">{{ t('usage.model') }}</th>
                <th class="py-3 pr-4">{{ t('usage.provider_requests_total') }}</th>
                <th class="py-3 pr-4">{{ t('usage.failed_requests') }}</th>
                <th class="py-3 pr-4">{{ t('usage.token_consumption_total') }}</th>
                <th class="py-3 pr-4 group relative">
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

        <div v-if="activeUsageTab === 'coverage'" class="app-panel p-5 rounded-lg overflow-x-auto">
          <div class="mb-4 text-sm font-bold uppercase tracking-widest text-text-secondary">{{ t('usage.coverage') }}</div>
          <table class="min-w-[1400px] w-full text-sm">
            <thead>
              <tr class="border-b border-border text-left text-xs uppercase tracking-widest text-text-secondary">
                <th class="py-3 pr-4">{{ t('usage.provider') }}</th>
                <th class="py-3 pr-4">API URL</th>
                <th class="py-3 pr-4">{{ t('usage.model') }}</th>
                <th class="py-3 pr-4">{{ t('usage.source_entrypoint') }}</th>
                <th class="py-3 pr-4">{{ t('usage.provider_requests_total') }}</th>
                <th class="py-3 pr-4 group relative">
                  <span class="inline-flex items-center gap-1.5">
                    <span>{{ t('usage.usage_coverage') }}</span>
                    <UsageCoverageHelp placement="bottom" />
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

      <div v-if="activeTab === 'sessions'">
        <SessionBrowser />
      </div>
    </div>

    <div v-if="showUsageClearModal" class="fixed inset-0 z-50 flex items-center justify-center bg-black/45 px-4">
      <div class="app-panel w-full max-w-lg rounded-lg p-6 shadow-xl">
        <h3 class="text-lg font-bold">{{ t('usage.clear_data_title') }}</h3>
        <p class="mt-3 text-sm leading-6 text-text-secondary">{{ t('usage.clear_data_confirm') }}</p>
        <label class="mt-5 flex items-start gap-3 text-sm">
          <input v-model="resetUsageSessionSync" type="checkbox" class="mt-1" />
          <span>
            <span class="block font-semibold">{{ t('usage.clear_data_reset_session_sync') }}</span>
            <span class="mt-1 block text-text-secondary">{{ t('usage.clear_data_reset_session_sync_hint') }}</span>
          </span>
        </label>
        <div class="mt-6 flex justify-end gap-2">
          <button class="app-control rounded-lg px-4 py-2 text-sm font-semibold" :disabled="usageClearLoading" @click="closeUsageClearModal">
            {{ t('modal.cancel') }}
          </button>
          <button class="rounded-lg border-none bg-danger px-4 py-2 text-sm font-semibold text-white disabled:cursor-not-allowed disabled:opacity-60" :disabled="usageClearLoading" @click="confirmUsageClear">
            {{ t('usage.clear_data') }}
          </button>
        </div>
      </div>
    </div>

    <ProviderModal v-if="showModal" :provider="editingProvider" @close="closeModal" @saved="handleSaved" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import type { EChartsType } from 'echarts/core'
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
import { useTheme } from '@/composables/useTheme'
import AppHeader from '@/components/AppHeader.vue'
import ProviderCard from '@/components/ProviderCard.vue'
import ProviderModal from '@/components/ProviderModal.vue'
import UsageCoverageHelp from '@/components/UsageCoverageHelp.vue'
import UsageStatsScopeHelp from '@/components/UsageStatsScopeHelp.vue'
import SessionBrowser from '@/components/SessionBrowser.vue'
import { formatPercent } from '@/utils/formatters'

const router = useRouter()
const api = useApi()
const { t, locale } = useI18n()
const { syncTheme, themeMode } = useTheme()

type MainTab = 'status' | 'providers' | 'connection' | 'certs' | 'usage' | 'sessions'
type UsageTab = 'overview' | 'requests' | 'providers' | 'models' | 'coverage'
type UsageDateRangePreset = 'today' | 'last_7_days' | 'last_30_days'

const tabs: Array<{ key: MainTab; labelKey: string }> = [
  { key: 'status', labelKey: 'tab.status' },
  { key: 'providers', labelKey: 'tab.providers' },
  { key: 'connection', labelKey: 'tab.connection' },
  { key: 'certs', labelKey: 'tab.certs' },
  { key: 'usage', labelKey: 'tab.usage' },
  { key: 'sessions', labelKey: 'tab.sessions' },
]

const usageTabs: Array<{ key: UsageTab; labelKey: string }> = [
  { key: 'overview', labelKey: 'usage.overview' },
  { key: 'requests', labelKey: 'usage.requests' },
  { key: 'providers', labelKey: 'usage.providers' },
  { key: 'models', labelKey: 'usage.models' },
  { key: 'coverage', labelKey: 'usage.coverage' },
]

const usageDateRangePresets: Array<{ key: UsageDateRangePreset; labelKey: string }> = [
  { key: 'today', labelKey: 'usage.range_today' },
  { key: 'last_7_days', labelKey: 'usage.range_last_7_days' },
  { key: 'last_30_days', labelKey: 'usage.range_last_30_days' },
]

const activeTab = ref<MainTab>('status')
const activeUsageTab = ref<UsageTab>('overview')
const containerClass = computed(() => 'max-w-[1600px]')

const status = ref<StatusInfo | null>(null)
const providers = ref<Provider[]>([])
const activeProviderId = ref('')
const certs = ref<CertificateInfo | null>(null)
const configuredMode = ref<'transparent' | 'tunnel' | 'gateway'>('transparent')
const effectiveMode = ref<'transparent' | 'tunnel' | 'gateway'>('transparent')
const modeRationale = ref('')
const viewMode = ref<'transparent' | 'tunnel' | 'gateway'>('transparent')
const modeSaving = ref(false)
const modeMessage = ref('')
const modeMessageIsError = ref(false)
const copiedSettings = ref(false)
const gatewayAddr = ref('127.0.0.1')
const gatewayPort = ref(17487)
const gatewayAddrInput = ref('127.0.0.1')
const gatewayPortInput = ref(17487)
const gatewaySaving = ref(false)
const gatewayMessage = ref('')
const gatewayMessageIsError = ref(false)
const modeOptions = [
  { value: 'transparent' as const, label: () => t('mode.transparent.title'), hint: () => t('mode.transparent.hint') },
  { value: 'tunnel' as const, label: () => t('mode.tunnel.title'), hint: () => t('mode.tunnel.hint') },
  { value: 'gateway' as const, label: () => t('mode.gateway.title'), hint: () => t('mode.gateway.hint') },
]

const usageSummary = ref<UsageSummary | null>(null)
const usageTrends = ref<UsageTrendPoint[]>([])
const usageRequests = ref<UsageRequestPage | null>(null)
const usageRequestPage = ref(1)
const usageRequestPageSize = ref(10)
const usageRequestPageSizes = [10, 20, 50, 100]
const usageProviders = ref<UsageAggregateRow[]>([])
const usageModels = ref<UsageAggregateRow[]>([])
const usageCoverage = ref<UsageCoverageRow[]>([])
const usageLoading = ref(false)
const usageChartEl = ref<HTMLDivElement | null>(null)
let echartsModule: { init: (dom: HTMLDivElement) => EChartsType } | null = null
let usageChart: EChartsType | null = null
let statusRefreshTimer: number | null = null
const defaultUsageDateRange = usageDateRangeForPreset('last_7_days')

const usageFilters = reactive({
  from: defaultUsageDateRange.from,
  to: defaultUsageDateRange.to,
  provider_id: 'all',
  model: 'all',
  status: 'all',
  usage_source: 'all',
  stats_scope: 'effective',
  source_entrypoint: 'all',
  q: '',
  tz: browserTimeZone(),
})

const activeProvider = computed(() => providers.value.find((p) => p.id === activeProviderId.value))
const providerOptions = computed(() => providers.value.map((provider) => ({ value: provider.id, label: provider.name })))
const modelOptions = computed(() => uniqueValues(usageModels.value.map((row) => row.mapped_model || row.name)))
const activeUsageDateRangePreset = computed(() => {
  for (const preset of usageDateRangePresets) {
    const range = usageDateRangeForPreset(preset.key)
    if (usageFilters.from === range.from && usageFilters.to === range.to) {
      return preset.key
    }
  }
  return ''
})
const usageRequestTotalPages = computed(() =>
  Math.max(1, Math.ceil((usageRequests.value?.total ?? 0) / usageRequestPageSize.value))
)

const showModal = ref(false)
const editingProvider = ref<Provider | null>(null)
const showUsageClearModal = ref(false)
const resetUsageSessionSync = ref(false)
const usageClearLoading = ref(false)

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

function openUsageClearModal() {
  resetUsageSessionSync.value = false
  showUsageClearModal.value = true
}

function closeUsageClearModal() {
  if (usageClearLoading.value) return
  showUsageClearModal.value = false
  resetUsageSessionSync.value = false
}

function goToUsageRequestPage(page: number) {
  usageRequestPage.value = Math.max(1, Math.min(page, usageRequestTotalPages.value))
  void loadUsageData()
}

function applyUsageDateRangePreset(preset: UsageDateRangePreset) {
  const range = usageDateRangeForPreset(preset)
  usageFilters.from = range.from
  usageFilters.to = range.to
}

async function confirmUsageClear() {
  usageClearLoading.value = true
  try {
    await api.clearUsageData(resetUsageSessionSync.value)
    showUsageClearModal.value = false
    resetUsageSessionSync.value = false
    usageRequestPage.value = 1
    await loadUsageData()
    alert(t('usage.clear_data_success'))
  } catch {
    alert(t('usage.clear_data_failed'))
  } finally {
    usageClearLoading.value = false
  }
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
    status.value = await api.getStatus(browserTimeZone())
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

function normalizeMode(mode: string | undefined | null) {
  if (mode === 'tunnel' || mode === 'gateway') return mode
  return 'transparent'
}

function modeTitle(mode: string) {
  switch (normalizeMode(mode)) {
    case 'tunnel':
      return t('mode.tunnel.title')
    case 'gateway':
      return t('mode.gateway.title')
    default:
      return t('mode.transparent.title')
  }
}

async function saveMode(mode: 'transparent' | 'tunnel' | 'gateway') {
  if (configuredMode.value === mode) {
    modeMessage.value = t('mode.already_selected')
    modeMessageIsError.value = false
    return
  }
  modeSaving.value = true
  modeMessage.value = ''
  modeMessageIsError.value = false
  try {
    const result = await api.updateConfig({ connection_mode: mode })
    configuredMode.value = normalizeMode(result.connection_mode)
    modeMessage.value = t('mode.saved')
    modeMessageIsError.value = false
    window.dispatchEvent(new CustomEvent('mcc:mode-updated'))
  } catch {
    modeMessage.value = t('mode.save_failed')
    modeMessageIsError.value = true
  } finally {
    modeSaving.value = false
  }
}

async function loadConnectionMode() {
  try {
    const [status, config] = await Promise.all([api.getStatus(), api.getConfig()])
    configuredMode.value = normalizeMode(config.connection_mode || status.configured_mode || 'transparent')
    effectiveMode.value = normalizeMode(status.effective_mode || status.configured_mode || config.connection_mode || 'transparent')
    modeRationale.value = status.mode_rationale || ''
    viewMode.value = configuredMode.value
    if (config.gateway_listen_addr) gatewayAddr.value = config.gateway_listen_addr
    if (config.gateway_listen_port) gatewayPort.value = config.gateway_listen_port
    gatewayAddrInput.value = gatewayAddr.value
    gatewayPortInput.value = gatewayPort.value
  } catch {
    // best-effort
  }
}

function copySettings() {
  const json = viewMode.value === 'gateway' ? gatewaySettingsJson.value : t(`mode.${viewMode.value}.settings_json`)
  navigator.clipboard.writeText(json)
  copiedSettings.value = true
  setTimeout(() => { copiedSettings.value = false }, 2000)
}

const gatewaySettingsJson = computed(() => {
  const baseUrl = `http://${gatewayAddr.value}:${gatewayPort.value}`
  return `{\n  "env": {\n    "ANTHROPIC_AUTH_TOKEN": "sk-sp-1858xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxc0ed",\n    "ANTHROPIC_BASE_URL": "${baseUrl}"\n  }\n}`
})

// 推荐配置：根据当前模式生成 env 代理地址，其余（插件/marketplace/hooks/权限）固定。
// 路径用 $HOME / 相对占位，避免硬编码用户家目录。
const recommendedConfigJson = computed(() => {
  const env: Record<string, string> = {
    ANTHROPIC_AUTH_TOKEN: 'sk-sp-1858xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxc0ed',
    ANTHROPIC_DEFAULT_HAIKU_MODEL: 'claude-haiku-4-5',
    ANTHROPIC_DEFAULT_OPUS_MODEL: 'claude-opus-4-8[1m]',
    ANTHROPIC_DEFAULT_SONNET_MODEL: 'claude-sonnet-4-8[1m]',
    ANTHROPIC_MODEL: 'claude-opus-4-8[1m]',
    ANTHROPIC_REASONING_MODEL: 'claude-opus-4-8[1m]',
    CLAUDE_AUTOCOMPACT_PCT_OVERRIDE: '75',
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
    CLAUDE_CODE_EFFORT_LEVEL: 'max',
    CLAUDE_CODE_SUBAGENT_MODEL: 'claude-haiku-4-5',
    DISABLE_AUTOUPDATER: '1',
    ENABLE_TOOL_SEARCH: 'true',
  }
  if (viewMode.value === 'gateway') {
    env.ANTHROPIC_BASE_URL = `http://${gatewayAddr.value}:${gatewayPort.value}`
  } else {
    env.ANTHROPIC_BASE_URL = 'https://api.anthropic.com'
    if (viewMode.value === 'tunnel') {
      env.HTTPS_PROXY = 'https://127.0.0.1:443'
      env.NODE_EXTRA_CA_CERTS = '/absolute/path/to/data/ca.crt'
    }
  }
  const config = {
    enabledPlugins: {
      'agent-sdk-dev@claude-plugins-official': true,
      'andrej-karpathy-skills@karpathy-skills': true,
      'chrome-devtools-mcp@claude-plugins-official': true,
      'clangd-lsp@claude-plugins-official': true,
      'claude-code-setup@claude-plugins-official': true,
      'claude-hud@claude-hud': true,
      'claude-md-management@claude-plugins-official': true,
      'code-review@claude-plugins-official': true,
      'code-simplifier@claude-plugins-official': true,
      'commit-commands@claude-plugins-official': true,
      'feature-dev@claude-plugins-official': true,
      'firecrawl@claude-plugins-official': true,
      'frontend-design@claude-plugins-official': true,
      'gopls-lsp@claude-plugins-official': true,
      'jdtls-lsp@claude-plugins-official': true,
      'learning-output-style@claude-plugins-official': true,
      'lua-lsp@claude-plugins-official': true,
      'mcp-server-dev@claude-plugins-official': true,
      'minimax-skills@minimax-skills': true,
      'php-lsp@claude-plugins-official': true,
      'playwright@claude-plugins-official': false,
      'plugin-dev@claude-plugins-official': true,
      'pr-review-toolkit@claude-plugins-official': true,
      'pyright-lsp@claude-plugins-official': true,
      'ralph-loop@claude-plugins-official': true,
      'rust-analyzer-lsp@claude-plugins-official': true,
      'rust-skills@rust-skills': true,
      'security-guidance@claude-plugins-official': true,
      'skill-creator@claude-plugins-official': true,
      'superpowers@claude-plugins-official': true,
      'typescript-lsp@claude-plugins-official': true,
      'ui-ux-pro-max@ui-ux-pro-max-skill': true,
    },
    env,
    extraKnownMarketplaces: {
      'claude-hud': { source: { repo: 'jarrodwatts/claude-hud', source: 'github' } },
      'karpathy-skills': { source: { repo: 'forrestchang/andrej-karpathy-skills', source: 'github' } },
      'ui-ux-pro-max-skill': { source: { repo: 'nextlevelbuilder/ui-ux-pro-max-skill', source: 'github' } },
    },
    hooks: {
      PreToolUse: [
        { hooks: [{ command: 'rtk hook claude', type: 'command' }], matcher: 'Bash' },
      ],
    },
    includeCoAuthoredBy: false,
    model: 'opus[1m]',
    permissions: { defaultMode: 'bypassPermissions' },
    skipDangerousModePermissionPrompt: true,
    statusLine: {
      command: 'bash -c \'export COLUMNS=120; exec "$HOME/.bun/bin/bun" --env-file /dev/null "$HOME/.claude/plugins/cache/claude-hud/claude-hud/0.1.0/src/index.ts"\'',
      type: 'command',
    },
    teammateMode: 'auto',
  }
  return JSON.stringify(config, null, 2)
})

const copiedRecommended = ref(false)
function copyRecommended() {
  navigator.clipboard.writeText(recommendedConfigJson.value)
  copiedRecommended.value = true
  setTimeout(() => { copiedRecommended.value = false }, 2000)
}

async function saveGatewaySettings() {
  if (!gatewayAddrInput.value.trim()) {
    gatewayMessage.value = t('mode.gateway.invalid_addr')
    gatewayMessageIsError.value = true
    return
  }
  if (gatewayPortInput.value < 1024 || gatewayPortInput.value > 65535) {
    gatewayMessage.value = t('mode.gateway.invalid_port')
    gatewayMessageIsError.value = true
    return
  }
  gatewaySaving.value = true
  gatewayMessage.value = ''
  gatewayMessageIsError.value = false
  try {
    const result = await api.updateConfig({
      gateway_listen_addr: gatewayAddrInput.value.trim(),
      gateway_listen_port: gatewayPortInput.value,
    })
    gatewayAddr.value = result.gateway_listen_addr || gatewayAddrInput.value.trim()
    gatewayPort.value = result.gateway_listen_port || gatewayPortInput.value
    gatewayAddrInput.value = gatewayAddr.value
    gatewayPortInput.value = gatewayPort.value
    if (result.gateway_restart_failed) {
      gatewayMessage.value = t('mode.gateway.restart_failed') + ': ' + result.gateway_restart_failed
      gatewayMessageIsError.value = true
    } else {
      gatewayMessage.value = t('mode.gateway.settings_saved')
      gatewayMessageIsError.value = false
    }
  } catch {
    gatewayMessage.value = t('mode.save_failed')
    gatewayMessageIsError.value = true
  } finally {
    gatewaySaving.value = false
  }
}

async function loadUsageData() {
  if (activeTab.value !== 'usage') return
  usageLoading.value = true
  try {
    const params = { ...usageFilters, stats_scope: usageFilters.stats_scope }
    const [summary, trends, requests, providers, models, coverage] = await Promise.all([
      api.getUsageSummary(params),
      api.getUsageTrends(params),
      api.getUsageRequests({ ...params, page: usageRequestPage.value, page_size: usageRequestPageSize.value }),
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
    await updateUsageChart()
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

function formatDuration(value: number | null | undefined): string {
  if (value === null || value === undefined) return '-'
  return `${formatNumber(value)} ms`
}

function formatEntrypoint(value: string): string {
  if (!value) return '-'
  if (value === 'cli') return t('usage.source_cli')
  if (value === 'claude-vscode') return t('usage.source_claude_vscode')
  if (value === 'unknown') return t('usage.source_unknown')
  return value
}

function formatUsageSource(value: UsageRequestRow['usage_source']): string {
  if (value === 'provider') return t('usage.usage_source_provider')
  if (value === 'session_log') return t('usage.usage_source_session_log')
  return t('usage.usage_source_none')
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

function dateTimeInputStartOfDaysAgo(days: number): string {
  const d = new Date()
  d.setDate(d.getDate() - days)
  return formatDateTimeInput(startOfLocalDay(d))
}

function dateTimeInputEndOfDaysAgo(days: number): string {
  const d = new Date()
  d.setDate(d.getDate() - days)
  return formatDateTimeInput(endOfLocalDay(d))
}

function startOfLocalDay(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate(), 0, 0, 0, 0)
}

function endOfLocalDay(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate(), 23, 59, 59, 0)
}

function formatDateTimeInput(date: Date): string {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  const hour = String(date.getHours()).padStart(2, '0')
  const minute = String(date.getMinutes()).padStart(2, '0')
  const second = String(date.getSeconds()).padStart(2, '0')
  return `${year}-${month}-${day}T${hour}:${minute}:${second}`
}

function inclusiveDateTimeRange(startDaysAgo: number, endDaysAgo: number): { from: string; to: string } {
  return {
    from: dateTimeInputStartOfDaysAgo(startDaysAgo),
    to: dateTimeInputEndOfDaysAgo(endDaysAgo),
  }
}

function usageDateRangeForPreset(preset: UsageDateRangePreset): { from: string; to: string } {
  switch (preset) {
    case 'today':
      return inclusiveDateTimeRange(0, 0)
    case 'last_7_days':
      return inclusiveDateTimeRange(7, 1)
    case 'last_30_days':
      return inclusiveDateTimeRange(30, 1)
  }
}

function uniqueValues(values: string[]): string[] {
  return Array.from(new Set(values.filter(Boolean))).sort((a, b) => a.localeCompare(b))
}

function appCssVar(name: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback
  const value = window.getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return value || fallback
}

function usageChartTheme() {
  return {
    text: appCssVar('--app-text', '#102033'),
    muted: appCssVar('--app-text-muted', '#64748b'),
    surface: appCssVar('--app-surface-raised', '#ffffff'),
    border: appCssVar('--app-border', '#dbeafe'),
    accent: appCssVar('--app-accent', '#2563eb'),
  }
}

async function loadECharts() {
  if (!echartsModule) {
    const [core, charts, components, renderers] = await Promise.all([
      import('echarts/core'),
      import('echarts/charts'),
      import('echarts/components'),
      import('echarts/renderers'),
    ])
    core.use([
      charts.LineChart,
      components.GridComponent,
      components.TooltipComponent,
      components.LegendComponent,
      components.GraphicComponent,
      renderers.CanvasRenderer,
    ])
    echartsModule = { init: core.init }
  }
  return echartsModule
}

async function updateUsageChart() {
  if (activeTab.value !== 'usage' || activeUsageTab.value !== 'overview') {
    disposeUsageChart()
    return
  }
  if (!usageChartEl.value) return
  const echarts = await loadECharts()
  if (activeTab.value !== 'usage' || activeUsageTab.value !== 'overview' || !usageChartEl.value) return
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

  const theme = usageChartTheme()
  usageChart.setOption({
    color: [theme.accent, '#fb7185', '#22c55e', '#f59e0b', '#a855f7', '#14b8a6', '#38bdf8'],
    tooltip: {
      trigger: 'axis',
      backgroundColor: theme.surface,
      borderColor: theme.border,
      textStyle: { color: theme.text },
    },
    legend: { top: 0, textStyle: { color: theme.muted } },
    grid: { left: 48, right: 56, top: 48, bottom: 32 },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: data.map((row) => row.bucket),
      axisLabel: { color: theme.muted },
      axisLine: { lineStyle: { color: theme.border } },
      splitLine: { lineStyle: { color: theme.border } },
    },
    yAxis: [
      {
        type: 'value',
        name: t('usage.provider_requests_total'),
        nameTextStyle: { color: theme.muted },
        axisLabel: { color: theme.muted },
        axisLine: { lineStyle: { color: theme.border } },
        splitLine: { lineStyle: { color: theme.border } },
      },
      {
        type: 'value',
        name: t('usage.usage_coverage'),
        min: 0,
        max: 1,
        nameTextStyle: { color: theme.muted },
        axisLabel: { color: theme.muted, formatter: (v: number) => `${Math.round(v * 100)}%` },
        axisLine: { lineStyle: { color: theme.border } },
        splitLine: { lineStyle: { color: theme.border } },
      },
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

function handleUsageChartResize() {
  void updateUsageChart()
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
    await updateUsageChart()
  }
)

watch(
  usageFilters,
  () => {
    usageRequestPage.value = 1
    scheduleUsageLoad()
  },
  { deep: true }
)

watch(usageRequestPageSize, () => {
  usageRequestPage.value = 1
  void loadUsageData()
})

watch(themeMode, () => {
  void updateUsageChart()
})

onMounted(async () => {
  await syncTheme(api.getPreferences)
  await Promise.all([loadStatus(), loadProviders(), loadCerts(), loadConnectionMode()])
  void loadUsageData()
  statusRefreshTimer = window.setInterval(() => {
    void loadStatus()
  }, 30000)
  window.addEventListener('resize', handleUsageChartResize)
})

onBeforeUnmount(() => {
  if (statusRefreshTimer) window.clearInterval(statusRefreshTimer)
  if (filterTimer) window.clearTimeout(filterTimer)
  window.removeEventListener('resize', handleUsageChartResize)
  disposeUsageChart()
})
</script>
