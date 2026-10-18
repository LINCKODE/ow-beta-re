import { computed, reactive } from 'vue'
import { api } from './api'
import { useExplorer } from './useExplorer'

// Panel sizes (px) by id: live values here, remembered in the app state (state.json, App.SetPanel).
const live = reactive<Record<string, number>>({})

// usePanel returns the size of a panel as a writable computed with commit() (save it) .
export function usePanel(id: string, def: number) {
  const { s } = useExplorer()
  const c = computed<number>({
    get: () => live[id] ?? s.ui.panels?.[id] ?? def,
    set: (v) => { live[id] = v },
  }) as any
  c.commit = async (reset = false) => {
    if (reset) delete live[id]
    s.ui = await api.SetPanel(id, reset ? 0 : live[id] ?? def)
  }
  return c as typeof c & { commit: (reset?: boolean) => Promise<void> }
}
