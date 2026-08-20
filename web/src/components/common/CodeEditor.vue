<script setup lang="ts">
/**
 * 代码编辑器：CodeMirror 6 懒加载封装。
 *
 * - 懒加载：首次可见时动态 import CM6 各包（独立 chunk，不进首包）。
 * - 双向绑定：v-model（外部变更同步到 doc，仅当不同时 dispatch 避免光标跳）。
 * - 语言切换：用 Compartment reconfigure，不重建实例。
 * - 深色主题对齐脚本编辑器 #0b1020 背景。
 * - 行号、括号匹配、自动缩进、历史（撤销/重做）、Tab 缩进。
 */
import { markRaw, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import type { Compartment, Extension } from '@codemirror/state'
import type { EditorView } from '@codemirror/view'

/**
 * 懒加载得到的 CM6 模块集合。
 *
 * 用 `typeof import(...)` 取真实类型：它只存在于类型位置，不会产生运行时导入，
 * 因此既保留独立 chunk 懒加载，又让 TS 能校验 API 用法。
 * 曾经这里是手写的 unknown 结构体加 `as unknown as` 双重断言，把 Facet
 * （EditorView.updateListener）误标成函数，编译期查不出来，运行期直接抛
 * TypeError 导致编辑器永远停在加载中。
 */
type CMModules = {
  state: typeof import('@codemirror/state')
  view: typeof import('@codemirror/view')
  commands: typeof import('@codemirror/commands')
  language: typeof import('@codemirror/language')
  python: typeof import('@codemirror/lang-python')
  javascript: typeof import('@codemirror/lang-javascript')
  highlight: typeof import('@lezer/highlight')
}

const props = defineProps<{
  modelValue: string
  language: 'python' | 'javascript'
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const hostRef = ref<HTMLElement | null>(null)
const loaded = ref(false)
const loadError = ref('')
const editorRef = shallowRef<EditorView | null>(null)
let loadPromise: Promise<void> | null = null
let langCompartment: Compartment | null = null
let cm: CMModules | null = null
let suppressEmit = false

async function loadCodeMirror(): Promise<void> {
  if (loaded.value) return
  if (loadPromise != null) {
    await loadPromise
    return
  }
  loadError.value = ''
  loadPromise = (async () => {
    // 动态导入各 CM6 包（合并为独立 chunk，不进首包）。
    const [state, view, commands, language, python, javascript, highlight] = await Promise.all([
      import('@codemirror/state'),
      import('@codemirror/view'),
      import('@codemirror/commands'),
      import('@codemirror/language'),
      import('@codemirror/lang-python'),
      import('@codemirror/lang-javascript'),
      import('@lezer/highlight'),
    ])
    cm = { state, view, commands, language, python, javascript, highlight }
    buildEditor(cm)
    loaded.value = true
  })()
  try {
    await loadPromise
  } catch (err) {
    // 不能静默失败：否则界面永远停在「编辑器加载中」且无任何提示。
    loadError.value = err instanceof Error ? err.message : '编辑器加载失败'
    loadPromise = null
  }
}

function languageExtension(mods: CMModules, lang: string): Extension {
  return lang === 'javascript' ? mods.javascript.javascript() : mods.python.python()
}

/** 深色高亮配色，与脚本编辑器 #0b1020 深底对齐。 */
function darkHighlightStyle(mods: CMModules): Extension {
  const { tags } = mods.highlight
  const style = mods.language.HighlightStyle.define([
    { tag: tags.keyword, color: '#c792ea' },
    { tag: [tags.controlKeyword, tags.moduleKeyword], color: '#c792ea' },
    { tag: [tags.string, tags.special(tags.string)], color: '#c3e88d' },
    { tag: [tags.number, tags.bool, tags.null], color: '#f78c6c' },
    {
      tag: [tags.comment, tags.lineComment, tags.blockComment],
      color: '#5c6a86',
      fontStyle: 'italic',
    },
    { tag: [tags.function(tags.variableName), tags.function(tags.propertyName)], color: '#82aaff' },
    { tag: [tags.definition(tags.variableName), tags.className], color: '#82aaff' },
    { tag: [tags.operator, tags.punctuation, tags.bracket], color: '#89ddff' },
    { tag: [tags.variableName, tags.propertyName], color: '#e2e8f0' },
    { tag: tags.typeName, color: '#ffcb6b' },
    { tag: tags.invalid, color: '#ff5370' },
  ])
  return mods.language.syntaxHighlighting(style, { fallback: true })
}

function buildEditor(mods: CMModules): void {
  if (!hostRef.value) return
  const { EditorView, keymap, lineNumbers, highlightActiveLineGutter, highlightSpecialChars } =
    mods.view
  langCompartment = new mods.state.Compartment()
  const editor = new EditorView({
    doc: props.modelValue,
    extensions: [
      lineNumbers(),
      highlightActiveLineGutter(),
      highlightSpecialChars(),
      mods.commands.history(),
      mods.language.bracketMatching(),
      mods.language.indentOnInput(),
      darkHighlightStyle(mods),
      keymap.of([
        ...mods.commands.defaultKeymap,
        ...mods.commands.historyKeymap,
        mods.commands.indentWithTab,
      ]),
      langCompartment.of(languageExtension(mods, props.language)),
      // updateListener 是 Facet，必须用 .of() 注册；直接调用会抛 TypeError。
      // 内容变化时 emit（避免回环：外部同步走 watch 的 suppressEmit）。
      EditorView.updateListener.of((update) => {
        if (update.docChanged && !suppressEmit) {
          emit('update:modelValue', update.state.doc.toString())
        }
      }),
    ],
    parent: hostRef.value,
  })
  editorRef.value = markRaw(editor)
}

onMounted(() => {
  void loadCodeMirror()
})

onUnmounted(() => {
  if (editorRef.value) {
    editorRef.value.destroy()
    editorRef.value = null
  }
  loadPromise = null
})

// 外部 modelValue 变化（如切换模板）→ 同步到 doc（仅当不同）。
watch(
  () => props.modelValue,
  (next) => {
    const ed = editorRef.value
    if (!ed || suppressEmit) return
    if (ed.state.doc.toString() === next) return
    suppressEmit = true
    ed.dispatch({
      changes: { from: 0, to: ed.state.doc.toString().length, insert: next || '' },
    })
    suppressEmit = false
  },
)

// 语言变化 → reconfigure（不重建实例）。
watch(
  () => props.language,
  (next) => {
    if (!langCompartment || !editorRef.value || !cm) return
    editorRef.value.dispatch({
      effects: langCompartment.reconfigure(languageExtension(cm, next)),
    })
  },
)
</script>

<template>
  <div class="code-editor-host">
    <div ref="hostRef" class="code-editor-cm" role="textbox" aria-label="脚本代码编辑器" />
    <!-- 遮罩绝对定位覆盖在宿主之上：宿主必须始终在 DOM 中供 CM 挂载，
         同时避免加载态与宿主各占 380px 把容器撑成两倍高。 -->
    <div
      v-if="!loaded"
      class="code-editor-overlay flex items-center justify-center rounded-lg bg-[#0b1020] px-4 text-xs"
    >
      <span v-if="loadError === ''" class="inline-flex items-center gap-2 text-gray-500">
        <span
          class="border-brand-400/70 h-3.5 w-3.5 animate-spin rounded-full border-2 border-t-transparent"
          aria-hidden="true"
        ></span>
        编辑器加载中…
      </span>
      <span v-else class="inline-flex flex-col items-center gap-2 text-center">
        <span class="text-error-400">编辑器加载失败：{{ loadError }}</span>
        <button
          type="button"
          class="text-brand-300 hover:text-brand-200 underline underline-offset-2"
          @click="loadCodeMirror"
        >
          重试
        </button>
      </span>
    </div>
  </div>
</template>

<style>
/* CodeMirror 编辑器容器：与脚本编辑器卡片视觉一致（#0b1020 深底）。 */
.code-editor-host {
  position: relative;
  min-height: 380px;
  width: 100%;
  display: flex;
  flex-direction: column;
}
.code-editor-overlay {
  position: absolute;
  inset: 0;
}
.code-editor-cm {
  min-height: 380px;
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
}
.code-editor-cm .cm-editor {
  min-height: 380px;
  flex: 1 1 auto;
  font-size: 13px;
  line-height: 1.5;
  background: #0b1020;
}
.code-editor-cm .cm-editor.cm-focused {
  outline: none;
}
.code-editor-cm .cm-scroller {
  font-family:
    ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, 'Liberation Mono', monospace;
}
.code-editor-cm .cm-gutters {
  background: #0b1020;
  border-right: 1px solid rgba(255, 255, 255, 0.06);
  color: #4b5670;
}
.code-editor-cm .cm-content {
  caret-color: #93c5fd;
  color: #e2e8f0;
}
.code-editor-cm .cm-activeLine,
.code-editor-cm .cm-activeLineGutter {
  background: rgba(255, 255, 255, 0.03);
}
/* 语法着色由 CM6 的 HighlightStyle 生成（见 darkHighlightStyle）；
   CM5 时代的 .cm-keyword/.cm-string 等类名在 CM6 下不会出现，故不再保留。 */
.code-editor-cm .cm-selectionBackground,
.code-editor-cm .cm-focused .cm-selectionBackground {
  background: rgba(130, 170, 255, 0.2);
}
</style>
