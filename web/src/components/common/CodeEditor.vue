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

type CMEditor = {
  destroy: () => void
  dispatch: (spec: unknown) => void
  state: { doc: { toString: () => string } }
  focus: () => void
  dom: HTMLElement
}

type CMPkg = {
  EditorView: {
    new (config: unknown): CMEditor
    create: (config: unknown) => CMEditor
    updateListener: (fn: (u: unknown) => void) => unknown
  }
  keymap: { of: (bindings: unknown[]) => unknown }
  lineNumbers: () => unknown
  highlightActiveLineGutter: () => unknown
  highlightSpecialChars: () => unknown
  bracketMatching: () => unknown
  indentOnInput: () => unknown
  history: () => unknown
  defaultKeymap: unknown[]
  historyKeymap: unknown[]
  indentWithTab: unknown
  Compartment: new () => { of: (ext: unknown) => unknown; reconfigure: (ext: unknown) => unknown }
  python: () => unknown
  javascript: () => unknown
}

const props = withDefaults(
  defineProps<{
    modelValue: string
    language: 'python' | 'javascript'
    placeholder?: string
    readonly?: boolean
  }>(),
  {
    placeholder: '',
    readonly: false,
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const hostRef = ref<HTMLElement | null>(null)
const loaded = ref(false)
const editorRef = shallowRef<CMEditor | null>(null)
let loadPromise: Promise<void> | null = null
let langCompartment: { of: (ext: unknown) => unknown; reconfigure: (ext: unknown) => unknown } | null = null
let loadedPkg: CMPkg | null = null
let suppressEmit = false

async function loadCodeMirror(): Promise<void> {
  if (loaded.value || loadPromise != null) {
    if (loadPromise != null) await loadPromise
    return
  }
  loadPromise = (async () => {
    // 动态导入各 CM6 包（进独立 chunk）。
    // CM6 模块分布：state(Compartment)、view(keymap/lineNumbers/highlight*/EditorView/updateListener)、
    // language(bracketMatching/indentOnInput)、commands(history/*Keymap/indentWithTab)、
    // lang-python/python、lang-javascript/javascript。
    const [stateMod, viewMod, cmdMod, langMod, pyMod, jsMod] = await Promise.all([
      import('@codemirror/state'),
      import('@codemirror/view'),
      import('@codemirror/commands'),
      import('@codemirror/language'),
      import('@codemirror/lang-python'),
      import('@codemirror/lang-javascript'),
    ])
    const pkg = {
      EditorView: viewMod.EditorView,
      keymap: viewMod.keymap,
      lineNumbers: viewMod.lineNumbers,
      highlightActiveLineGutter: viewMod.highlightActiveLineGutter,
      highlightSpecialChars: viewMod.highlightSpecialChars,
      bracketMatching: langMod.bracketMatching,
      indentOnInput: langMod.indentOnInput,
      history: cmdMod.history,
      defaultKeymap: cmdMod.defaultKeymap,
      historyKeymap: cmdMod.historyKeymap,
      indentWithTab: cmdMod.indentWithTab,
      Compartment: stateMod.Compartment,
      python: pyMod.python,
      javascript: jsMod.javascript,
    } as unknown as CMPkg
    loadedPkg = pkg
    buildEditor(pkg)
    loaded.value = true
  })()
  await loadPromise
}

function languageExtension(pkg: CMPkg, lang: string): unknown {
  return lang === 'javascript' ? pkg.javascript() : pkg.python()
}

function buildEditor(pkg: CMPkg): void {
  if (!hostRef.value) return
  langCompartment = new pkg.Compartment()
  const editor = new pkg.EditorView({
    doc: props.modelValue,
    extensions: [
      pkg.lineNumbers(),
      pkg.highlightActiveLineGutter(),
      pkg.highlightSpecialChars(),
      pkg.history(),
      pkg.bracketMatching(),
      pkg.indentOnInput(),
      pkg.keymap.of([...pkg.defaultKeymap, ...pkg.historyKeymap, pkg.indentWithTab]),
      langCompartment.of(languageExtension(pkg, props.language)),
      // updateListener：内容变化时 emit（避免回环：外部同步走 watch 的 suppressEmit）。
      pkg.EditorView.updateListener((vu) => {
        const u = vu as { docChanged: boolean; state: { doc: { toString: () => string } } }
        if (u.docChanged && !suppressEmit) {
          emit('update:modelValue', u.state.doc.toString())
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
    if (!langCompartment || !editorRef.value || !loadedPkg) return
    editorRef.value.dispatch({
      effects: langCompartment.reconfigure(languageExtension(loadedPkg, next)),
    })
  },
)
</script>

<template>
  <div class="code-editor-host">
    <div ref="hostRef" class="code-editor-cm" role="textbox" aria-label="脚本代码编辑器" />
    <div
      v-if="!loaded"
      class="flex min-h-[380px] items-center justify-center rounded-lg bg-[#0b1020] text-xs text-gray-500"
    >
      <span class="inline-flex items-center gap-2">
        <span
          class="h-3.5 w-3.5 animate-spin rounded-full border-2 border-brand-400/70 border-t-transparent"
          aria-hidden="true"
        ></span>
        编辑器加载中…
      </span>
    </div>
  </div>
</template>

<style>
/* CodeMirror 编辑器容器：与脚本编辑器卡片视觉一致（#0b1020 深底）。 */
.code-editor-host {
  min-height: 380px;
  width: 100%;
  display: flex;
  flex-direction: column;
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
  font-family: ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, 'Liberation Mono', monospace;
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
/* 基础语法着色（与原手写高亮器配色接近） */
.code-editor-cm .cm-keyword {
  color: #c792ea;
}
.code-editor-cm .cm-string {
  color: #c3e88d;
}
.code-editor-cm .cm-number {
  color: #f78c6c;
}
.code-editor-cm .cm-comment {
  color: #5c6a86;
  font-style: italic;
}
.code-editor-cm .cm-variable,
.code-editor-cm .cm-property {
  color: #e2e8f0;
}
.code-editor-cm .cm-def,
.code-editor-cm .cm-function {
  color: #82aaff;
}
.code-editor-cm .cm-operator {
  color: #89ddff;
}
.code-editor-cm .cm-punctuation {
  color: #89ddff;
}
.code-editor-cm .cm-selectionBackground {
  background: rgba(130, 170, 255, 0.2);
}
</style>
