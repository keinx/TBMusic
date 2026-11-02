export const safeStorage = {
  getItem(key: string): string | null {
    try { return localStorage.getItem(key) } catch { return null }
  },
  setItem(key: string, value: string) {
    try { localStorage.setItem(key, value) } catch { /* storage disabled or full */ }
  },
  removeItem(key: string) {
    try { localStorage.removeItem(key) } catch { /* storage disabled */ }
  },
}
