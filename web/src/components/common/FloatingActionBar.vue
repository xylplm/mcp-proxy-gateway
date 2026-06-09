<template>
  <div class="pointer-events-none fixed inset-x-0 bottom-0 z-99 px-4 pb-4 md:px-6">
    <div
      class="pointer-events-auto mx-auto flex w-full max-w-5xl flex-col gap-3 rounded-xl border border-gray-200 bg-white/95 p-3 shadow-theme-lg backdrop-blur sm:flex-row sm:items-center sm:justify-between dark:border-gray-800 dark:bg-gray-900/95"
    >
      <div class="min-w-0">
        <slot name="info" />
      </div>
      <div class="flex shrink-0 flex-wrap items-center justify-end gap-2">
        <slot />
      </div>
    </div>
  </div>
</template>
