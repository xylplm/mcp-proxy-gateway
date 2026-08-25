<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  type ComponentPublicInstance,
  useAttrs,
  watch,
} from 'vue'
import { CheckIcon, ChevronDownIcon } from '@/icons'

export type AppSelectValue = string | number

export interface AppSelectOption {
  value: AppSelectValue
  label: string
  description?: string
  keywords?: readonly string[]
  disabled?: boolean
}

type SelectSize = 'sm' | 'md' | 'lg'
type SearchableMode = boolean | 'auto'

const APP_SELECT_OPEN_EVENT = 'app-select:open'
let appSelectSequence = 0

defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    id?: string
    modelValue?: AppSelectValue | AppSelectValue[] | null
    options: readonly AppSelectOption[]
    multiple?: boolean
    searchable?: SearchableMode
    searchThreshold?: number
    filterable?: boolean
    filter?: (option: AppSelectOption, query: string) => boolean
    maxResults?: number
    placeholder?: string
    clearable?: boolean
    disabled?: boolean
    loading?: boolean
    size?: SelectSize
    ariaLabel?: string
    emptyText?: string
    noResultsText?: string
  }>(),
  {
    id: undefined,
    modelValue: null,
    multiple: false,
    searchable: 'auto',
    searchThreshold: 8,
    filterable: true,
    filter: undefined,
    maxResults: 100,
    placeholder: '请选择',
    clearable: false,
    disabled: false,
    loading: false,
    size: 'md',
    ariaLabel: undefined,
    emptyText: '暂无可选项',
    noResultsText: '没有匹配项',
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: AppSelectValue | AppSelectValue[] | null]
  change: [value: AppSelectValue | AppSelectValue[] | null]
  search: [query: string]
  open: []
  close: []
}>()

const attrs = useAttrs()
const rootRef = ref<HTMLElement | null>(null)
const triggerRef = ref<HTMLButtonElement | null>(null)
const panelRef = ref<HTMLElement | null>(null)
const listboxRef = ref<HTMLElement | null>(null)
const searchInputRef = ref<HTMLInputElement | null>(null)
const optionRefs = new Map<number, HTMLElement>()
const isOpen = ref(false)
const query = ref('')
const activeIndex = ref(-1)
const panelStyle = ref<Record<string, string>>({})
const instanceID = 'app-select-' + String(++appSelectSequence)
const triggerID = computed(() => props.id ?? instanceID)
const listboxID = instanceID + '-listbox'
let resizeObserver: ResizeObserver | null = null
let positionFrame: number | null = null

const rootClass = computed(() => {
  const className = attrs.class
  return className == null ? '' : className
})

const shouldShowSearch = computed(
  () =>
    props.searchable === true ||
    (props.searchable === 'auto' && props.options.length > props.searchThreshold),
)

const indexedOptions = computed(() =>
  props.options.map((option) => ({
    option,
    searchText: normalizeSearchText([option.label, option.description, ...(option.keywords ?? [])]),
  })),
)

const matchingOptions = computed(() => {
  const normalizedQuery = normalizeSearchText([query.value])
  if (!props.filterable || normalizedQuery === '') return indexedOptions.value
  return indexedOptions.value.filter(({ option, searchText }) =>
    props.filter ? props.filter(option, query.value) : searchText.includes(normalizedQuery),
  )
})

const visibleOptions = computed(() =>
  matchingOptions.value.slice(0, Math.max(1, props.maxResults)).map(({ option }) => option),
)

const hasMoreResults = computed(() => matchingOptions.value.length > visibleOptions.value.length)

const selectedValues = computed<AppSelectValue[]>(() => {
  if (props.multiple) {
    return Array.isArray(props.modelValue) ? props.modelValue : []
  }
  return props.modelValue == null || Array.isArray(props.modelValue) ? [] : [props.modelValue]
})

const selectedOptions = computed(() => {
  const selected = new Set(selectedValues.value)
  return props.options.filter((option) => selected.has(option.value))
})

const triggerLabel = computed(() => {
  if (selectedOptions.value.length === 0) return props.placeholder
  if (!props.multiple || selectedOptions.value.length === 1) return selectedOptions.value[0]!.label
  return '已选择 ' + String(selectedOptions.value.length) + ' 项'
})

const hasSelection = computed(() => selectedOptions.value.length > 0)

const sizeClass = computed(() => {
  switch (props.size) {
    case 'sm':
      return 'h-8 rounded-lg px-2.5 text-xs'
    case 'lg':
      return 'h-11 rounded-lg px-4 text-sm'
    default:
      return 'h-10 rounded-lg px-3 text-sm'
  }
})

const triggerClass = computed(() => [
  'flex w-full items-center gap-2 border border-gray-300 bg-transparent text-left text-gray-800 shadow-sm transition duration-150 ease-out focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none disabled:cursor-not-allowed disabled:opacity-60 dark:border-gray-700 dark:text-white/90',
  sizeClass.value,
  props.clearable && hasSelection.value ? 'pr-16' : 'pr-9',
  hasSelection.value ? '' : 'text-gray-400 dark:text-white/30',
])

function normalizeSearchText(values: readonly (string | undefined)[]): string {
  return values
    .filter((value): value is string => typeof value === 'string')
    .join(' ')
    .trim()
    .toLocaleLowerCase()
}

function isSelected(option: AppSelectOption): boolean {
  return selectedValues.value.includes(option.value)
}

function optionID(index: number): string {
  return instanceID + '-option-' + String(index)
}

function setOptionRef(index: number, element: Element | ComponentPublicInstance | null): void {
  if (element instanceof HTMLElement) {
    optionRefs.set(index, element)
    return
  }
  if (element != null && '$el' in element && element.$el instanceof HTMLElement) {
    optionRefs.set(index, element.$el)
    return
  }
  optionRefs.delete(index)
}

function firstEnabledIndex(): number {
  return visibleOptions.value.findIndex((option) => !option.disabled)
}

function selectedEnabledIndex(): number {
  return visibleOptions.value.findIndex((option) => isSelected(option) && !option.disabled)
}

function resetActiveIndex(): void {
  const selectedIndex = selectedEnabledIndex()
  activeIndex.value = selectedIndex >= 0 ? selectedIndex : firstEnabledIndex()
}

function moveActiveIndex(direction: 1 | -1): void {
  const options = visibleOptions.value
  if (options.length === 0) return

  let index = activeIndex.value
  for (let steps = 0; steps < options.length; steps += 1) {
    index = (index + direction + options.length) % options.length
    if (!options[index]!.disabled) {
      activeIndex.value = index
      void nextTick(() => optionRefs.get(index)?.scrollIntoView({ block: 'nearest' }))
      return
    }
  }
}

function moveToBoundary(last: boolean): void {
  const options = visibleOptions.value
  const start = last ? options.length - 1 : 0
  const direction = last ? -1 : 1
  for (let index = start; index >= 0 && index < options.length; index += direction) {
    if (!options[index]!.disabled) {
      activeIndex.value = index
      void nextTick(() => optionRefs.get(index)?.scrollIntoView({ block: 'nearest' }))
      return
    }
  }
}

function emitValue(value: AppSelectValue | AppSelectValue[] | null): void {
  emit('update:modelValue', value)
  emit('change', value)
}

function selectOption(option: AppSelectOption): void {
  if (option.disabled) return

  if (props.multiple) {
    const next = [...selectedValues.value]
    const currentIndex = next.indexOf(option.value)
    if (currentIndex >= 0) next.splice(currentIndex, 1)
    else next.push(option.value)
    emitValue(next)
    return
  }

  emitValue(option.value)
  closeDropdown()
}

function selectActiveOption(): void {
  const option = visibleOptions.value[activeIndex.value]
  if (option) selectOption(option)
}

function clearSelection(): void {
  if (props.disabled) return
  emitValue(props.multiple ? [] : null)
  query.value = ''
}

function updatePanelPosition(): void {
  const trigger = triggerRef.value
  if (!trigger || !isOpen.value) return

  const rect = trigger.getBoundingClientRect()
  const viewportMargin = 8
  const gap = 6
  const spaceBelow = window.innerHeight - rect.bottom - viewportMargin - gap
  const spaceAbove = rect.top - viewportMargin - gap
  const openAbove = spaceBelow < 180 && spaceAbove > spaceBelow
  const availableHeight = Math.max(96, Math.min(320, openAbove ? spaceAbove : spaceBelow))
  const width = Math.min(rect.width, window.innerWidth - viewportMargin * 2)
  const left = Math.max(
    viewportMargin,
    Math.min(rect.left, window.innerWidth - width - viewportMargin),
  )

  panelStyle.value = openAbove
    ? {
        bottom: String(Math.max(viewportMargin, window.innerHeight - rect.top + gap)) + 'px',
        left: String(left) + 'px',
        maxHeight: String(availableHeight) + 'px',
        width: String(width) + 'px',
      }
    : {
        top: String(Math.min(window.innerHeight - viewportMargin, rect.bottom + gap)) + 'px',
        left: String(left) + 'px',
        maxHeight: String(availableHeight) + 'px',
        width: String(width) + 'px',
      }
}

function schedulePanelPosition(): void {
  if (positionFrame !== null) return
  positionFrame = window.requestAnimationFrame(() => {
    positionFrame = null
    updatePanelPosition()
  })
}

function bindOpenListeners(): void {
  document.addEventListener('pointerdown', onDocumentPointerDown)
  document.addEventListener('scroll', schedulePanelPosition, true)
  window.addEventListener('resize', schedulePanelPosition)
  if (rootRef.value && typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(schedulePanelPosition)
    resizeObserver.observe(rootRef.value)
  }
}

function unbindOpenListeners(): void {
  document.removeEventListener('pointerdown', onDocumentPointerDown)
  document.removeEventListener('scroll', schedulePanelPosition, true)
  window.removeEventListener('resize', schedulePanelPosition)
  resizeObserver?.disconnect()
  resizeObserver = null
  if (positionFrame !== null) {
    window.cancelAnimationFrame(positionFrame)
    positionFrame = null
  }
}

function openDropdown(): void {
  if (props.disabled || isOpen.value) return
  window.dispatchEvent(new CustomEvent(APP_SELECT_OPEN_EVENT, { detail: instanceID }))
  query.value = ''
  resetActiveIndex()
  isOpen.value = true
  emit('open')
  void nextTick(() => {
    updatePanelPosition()
    if (shouldShowSearch.value) searchInputRef.value?.focus()
    else listboxRef.value?.focus()
  })
}

function closeDropdown(restoreFocus = false): void {
  if (!isOpen.value) return
  isOpen.value = false
  query.value = ''
  activeIndex.value = -1
  emit('close')
  if (restoreFocus) void nextTick(() => triggerRef.value?.focus())
}

function toggleDropdown(): void {
  if (isOpen.value) closeDropdown()
  else openDropdown()
}

function onDocumentPointerDown(event: PointerEvent): void {
  const target = event.target
  if (!(target instanceof Node)) return
  if (rootRef.value?.contains(target) || panelRef.value?.contains(target)) return
  closeDropdown()
}

function onAnotherSelectOpened(event: Event): void {
  if ((event as CustomEvent<string>).detail !== instanceID) closeDropdown()
}

function handleTriggerKeydown(event: KeyboardEvent): void {
  if (props.disabled) return
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    if (!isOpen.value) {
      openDropdown()
      if (event.key === 'ArrowUp') moveToBoundary(true)
    } else {
      moveActiveIndex(event.key === 'ArrowDown' ? 1 : -1)
    }
    return
  }
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    toggleDropdown()
  }
}

function handleOpenKeydown(event: KeyboardEvent): void {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    moveActiveIndex(1)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    moveActiveIndex(-1)
  } else if (event.key === 'Home') {
    event.preventDefault()
    moveToBoundary(false)
  } else if (event.key === 'End') {
    event.preventDefault()
    moveToBoundary(true)
  } else if (event.key === 'Enter') {
    event.preventDefault()
    selectActiveOption()
  } else if (event.key === 'Escape') {
    event.preventDefault()
    closeDropdown(true)
  } else if (event.key === 'Tab') {
    closeDropdown()
  }
}

watch(query, (value) => {
  emit('search', value)
  activeIndex.value = firstEnabledIndex()
})

watch(
  () => visibleOptions.value,
  () => {
    if (
      activeIndex.value < 0 ||
      activeIndex.value >= visibleOptions.value.length ||
      visibleOptions.value[activeIndex.value]?.disabled
    ) {
      activeIndex.value = firstEnabledIndex()
    }
  },
)

watch(isOpen, (open) => {
  if (open) bindOpenListeners()
  else unbindOpenListeners()
})

onMounted(() => {
  window.addEventListener(APP_SELECT_OPEN_EVENT, onAnotherSelectOpened)
})

onBeforeUnmount(() => {
  unbindOpenListeners()
  window.removeEventListener(APP_SELECT_OPEN_EVENT, onAnotherSelectOpened)
})
</script>

<template>
  <div ref="rootRef" class="relative min-w-0" :class="rootClass">
    <button
      :id="triggerID"
      ref="triggerRef"
      type="button"
      role="combobox"
      :class="triggerClass"
      :aria-controls="isOpen ? listboxID : undefined"
      :aria-expanded="isOpen"
      :aria-haspopup="'listbox'"
      :aria-label="ariaLabel ?? placeholder"
      :disabled="disabled"
      @click="toggleDropdown"
      @keydown="handleTriggerKeydown"
    >
      <span class="min-w-0 flex-1 truncate">{{ triggerLabel }}</span>
      <ChevronDownIcon
        class="h-4 w-4 shrink-0 text-gray-400 transition-transform duration-150 motion-reduce:transition-none dark:text-gray-500"
        :class="isOpen ? 'rotate-180' : ''"
        aria-hidden="true"
      />
    </button>

    <button
      v-if="clearable && hasSelection && !disabled"
      type="button"
      class="absolute top-1/2 right-7 inline-flex h-5 w-5 -translate-y-1/2 items-center justify-center rounded text-gray-400 transition hover:bg-gray-100 hover:text-gray-600 focus:ring-2 focus:ring-brand-500/20 focus:outline-none dark:hover:bg-white/10 dark:hover:text-gray-300"
      aria-label="清除选择"
      @click.stop="clearSelection"
    >
      <svg class="h-3.5 w-3.5" viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <path d="m4 4 8 8m0-8-8 8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
      </svg>
    </button>

    <Teleport to="body">
      <Transition
        enter-active-class="transition duration-150 ease-out motion-reduce:transition-none"
        enter-from-class="translate-y-1 scale-[0.98] opacity-0"
        enter-to-class="translate-y-0 scale-100 opacity-100"
        leave-active-class="transition duration-100 ease-in motion-reduce:transition-none"
        leave-from-class="translate-y-0 scale-100 opacity-100"
        leave-to-class="translate-y-1 scale-[0.98] opacity-0"
      >
        <div
          v-if="isOpen"
          ref="panelRef"
          class="fixed z-[100002] flex flex-col overflow-hidden rounded-xl border border-gray-200 bg-white p-1.5 shadow-xl dark:border-gray-700 dark:bg-gray-900"
          :style="panelStyle"
        >
          <div v-if="shouldShowSearch" class="shrink-0 border-b border-gray-100 p-1 pb-2 dark:border-gray-800">
            <input
              ref="searchInputRef"
              v-model="query"
              type="search"
              class="h-9 w-full rounded-lg border border-gray-200 bg-gray-50 px-3 text-sm text-gray-800 placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:bg-white/[0.04] dark:text-white/90 dark:placeholder:text-white/30"
              placeholder="搜索选项"
              autocomplete="off"
              spellcheck="false"
              :aria-controls="listboxID"
              :aria-activedescendant="activeIndex >= 0 ? optionID(activeIndex) : undefined"
              @keydown="handleOpenKeydown"
            />
          </div>

          <div
            :id="listboxID"
            ref="listboxRef"
            role="listbox"
            class="min-h-0 overflow-y-auto overscroll-contain py-0.5 outline-none"
            :aria-label="ariaLabel ?? placeholder"
            :aria-multiselectable="multiple || undefined"
            tabindex="0"
            @keydown="handleOpenKeydown"
          >
            <template v-if="!loading && visibleOptions.length > 0">
              <button
                v-for="(option, index) in visibleOptions"
                :id="optionID(index)"
                :key="String(option.value)"
                :ref="(element) => setOptionRef(index, element)"
                type="button"
                role="option"
                class="flex w-full items-start gap-3 rounded-lg px-3 py-2.5 text-left text-sm transition-colors duration-100 focus:outline-none motion-reduce:transition-none"
                :class="[
                  option.disabled
                    ? 'cursor-not-allowed opacity-45'
                    : 'cursor-pointer hover:bg-gray-100 dark:hover:bg-white/[0.06]',
                  isSelected(option)
                    ? 'bg-brand-50 text-brand-700 dark:bg-brand-500/10 dark:text-brand-300'
                    : 'text-gray-700 dark:text-gray-200',
                  activeIndex === index && !option.disabled
                    ? 'ring-1 ring-inset ring-brand-300 dark:ring-brand-500/50'
                    : '',
                ]"
                :aria-selected="isSelected(option)"
                :disabled="option.disabled"
                @mouseenter="activeIndex = index"
                @click="selectOption(option)"
              >
                <span
                  v-if="multiple"
                  class="mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded border transition-colors"
                  :class="
                    isSelected(option)
                      ? 'border-brand-500 bg-brand-500 text-white'
                      : 'border-gray-300 dark:border-gray-600'
                  "
                  aria-hidden="true"
                >
                  <CheckIcon v-if="isSelected(option)" class="h-3 w-3" />
                </span>
                <span class="min-w-0 flex-1">
                  <slot name="option" :option="option" :selected="isSelected(option)">
                    <span class="block truncate font-medium">{{ option.label }}</span>
                    <span
                      v-if="option.description"
                      class="mt-0.5 block line-clamp-2 text-xs leading-5 text-gray-500 dark:text-gray-400"
                    >
                      {{ option.description }}
                    </span>
                  </slot>
                </span>
                <CheckIcon
                  v-if="!multiple && isSelected(option)"
                  color="currentColor"
                  class="mt-0.5 h-4 w-4 shrink-0 text-brand-500"
                  aria-hidden="true"
                />
              </button>
            </template>

            <p
              v-else-if="loading"
              class="px-3 py-6 text-center text-sm text-gray-400 dark:text-gray-500"
            >
              加载中…
            </p>
            <p
              v-else
              class="px-3 py-6 text-center text-sm text-gray-400 dark:text-gray-500"
            >
              {{ query === '' ? emptyText : noResultsText }}
            </p>
          </div>

          <p
            v-if="hasMoreResults"
            class="shrink-0 border-t border-gray-100 px-3 py-2 text-center text-xs text-gray-400 dark:border-gray-800 dark:text-gray-500"
          >
            仅显示前 {{ maxResults }} 项，请继续输入缩小范围
          </p>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>
