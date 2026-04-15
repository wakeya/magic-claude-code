<template>
  <header class="bg-white border-b-2 border-border px-8 h-16 flex items-center justify-between">
    <div class="flex items-center gap-3.5">
      <div class="w-8 h-8 bg-primary rounded-md flex items-center justify-center">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 2L2 7l10 5 10-5-10-5z" /><path d="M2 17l10 5 10-5" /><path d="M2 12l10 5 10-5" />
        </svg>
      </div>
      <h1 class="text-[17px] font-bold tracking-tight">Claude Code Proxy</h1>
    </div>
    <button
      class="px-5 py-2 bg-transparent border-2 border-border text-text-secondary rounded-lg text-[13px] font-semibold cursor-pointer transition-all duration-200 hover:border-danger hover:text-danger hover:bg-danger-light"
      @click="$emit('logout')"
    >
      Logout
    </button>
  </header>
</template>

<script setup lang="ts">
defineEmits<{ logout: [] }>()
</script>
