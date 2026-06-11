<template>
  <ThemeProvider>
    <SidebarProvider>
      <RouterView />
      <ToastHost />
      <ConfirmDialog />
    </SidebarProvider>
  </ThemeProvider>
</template>

<script setup lang="ts">
import ThemeProvider from './components/layout/ThemeProvider.vue'
import SidebarProvider from './components/layout/SidebarProvider.vue'
import ToastHost from './components/common/ToastHost.vue'
import ConfirmDialog from './components/common/ConfirmDialog.vue'
</script>
