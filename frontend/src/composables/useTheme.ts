import { ref, watch } from 'vue'

export type Theme = 'light' | 'dark'

const theme = ref<Theme>(
  document.body.classList.contains('dark') ? 'dark' : 'light'
)

function applyTheme(t: Theme) {
  if (t === 'dark') {
    document.body.classList.add('dark')
  } else {
    document.body.classList.remove('dark')
  }
  localStorage.setItem('boss-theme', t)
}

watch(theme, applyTheme)

export function useTheme() {
  function toggle() {
    theme.value = theme.value === 'dark' ? 'light' : 'dark'
  }

  return { theme, toggle }
}
