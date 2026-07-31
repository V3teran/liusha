import '@testing-library/jest-dom/vitest'

// jsdom 不实现 ResizeObserver（浏览器 API），React Flow 内部依赖它监听画布尺寸变化。
// 测试环境用空实现 polyfill，只需不抛错，不需要真的触发回调。
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}
