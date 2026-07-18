<script setup lang="ts">
import { computed, ref } from 'vue'
import PathBrowserModal from '@/components/common/PathBrowserModal.vue'
import type { BrowseMode } from '@/api/fsbrowse'
import FolderIcon from '@/icons/FolderIcon.vue'
import { appendUniquePath } from '@/utils/pathPicker'

const props = withDefaults(
  defineProps<{
    modelValue: string
    mode?: BrowseMode
    multiple?: boolean
    placeholder?: string
    disabled?: boolean
    inputClass?: string
    inputId?: string
    rows?: number
    title?: string
    contextRoots?: string[]
    allowMissing?: boolean
  }>(),
  {
    mode: 'directory',
    multiple: false,
    placeholder: '',
    disabled: false,
    inputClass: '',
    inputId: undefined,
    rows: 3,
    title: '选择路径',
    contextRoots: () => [],
    allowMissing: true,
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const browserOpen = ref(false)

const defaultInputClass =
  'h-11 w-full rounded-lg border border-gray-300 bg-transparent px-4 pr-12 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30'

const mergedInputClass = computed(() => props.inputClass || defaultInputClass)

function openBrowser(): void {
  if (props.disabled) return
  browserOpen.value = true
}

function onSelect(path: string): void {
  if (props.multiple) {
    emit('update:modelValue', appendUniquePath(props.modelValue, path))
    return
  }
  emit('update:modelValue', path)
}
</script>

<template>
  <div class="relative">
    <textarea
      v-if="multiple"
      :id="inputId"
      :value="modelValue"
      :rows="rows"
      :disabled="disabled"
      :placeholder="placeholder"
      :class="[mergedInputClass, 'h-auto py-3 pr-12 font-mono']"
      @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
    ></textarea>
    <input
      v-else
      :id="inputId"
      :value="modelValue"
      type="text"
      :disabled="disabled"
      :placeholder="placeholder"
      :class="[mergedInputClass, 'pr-12 font-mono']"
      @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
    />
    <button
      type="button"
      class="hover:border-brand-200 hover:bg-brand-50 hover:text-brand-600 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/10 dark:hover:text-brand-300 absolute right-1.5 inline-flex h-8 w-8 items-center justify-center rounded-lg border border-gray-200 bg-white text-gray-500 shadow-sm transition disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-300"
      :class="multiple ? 'top-2' : 'top-1/2 -translate-y-1/2'"
      :disabled="disabled"
      :aria-label="multiple ? '浏览并添加路径' : '浏览选择路径'"
      @click="openBrowser"
    >
      <FolderIcon class="h-4 w-4" aria-hidden="true" />
    </button>

    <PathBrowserModal
      :open="browserOpen"
      :mode="mode"
      :title="title"
      :initial-path="multiple ? '' : modelValue"
      :context-roots="contextRoots"
      :allow-missing="allowMissing"
      @close="browserOpen = false"
      @select="onSelect"
    />
  </div>
</template>
