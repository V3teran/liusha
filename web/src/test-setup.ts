import '@testing-library/jest-dom/vitest'

// Web Storage polyfill。
// Node 22+ 在 globalThis 上原生挂了实验性 localStorage 描述符（无 --localstorage-file 时取值 undefined）。
// vitest 的 getWindowKeys 过滤逻辑为 `if (k in global) return KEYS.includes(k)`，而其 KEYS 白名单不含
// localStorage/sessionStorage——于是 jsdom 那个可用的实现被过滤掉，无法覆盖 Node 的坏值，测试侧取到 undefined。
// 用符合规范的内存实现显式覆盖，独立于 jsdom / Node 版本。
class MemoryStorage implements Storage {
  private store = new Map<string, string>()
  get length() {
    return this.store.size
  }
  clear() {
    this.store.clear()
  }
  getItem(key: string): string | null {
    return this.store.has(key) ? this.store.get(key)! : null
  }
  key(index: number): string | null {
    return Array.from(this.store.keys())[index] ?? null
  }
  removeItem(key: string) {
    this.store.delete(key)
  }
  setItem(key: string, value: string) {
    this.store.set(key, String(value))
  }
}

for (const name of ['localStorage', 'sessionStorage'] as const) {
  Object.defineProperty(globalThis, name, {
    value: new MemoryStorage(),
    configurable: true,
    writable: true,
  })
}

// jsdom 不实现 ResizeObserver（浏览器 API），React Flow 内部依赖它监听画布尺寸变化。
// 测试环境用空实现 polyfill，只需不抛错，不需要真的触发回调。
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}
