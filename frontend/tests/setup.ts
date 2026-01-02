import { vi } from 'vitest'

// Create storage mocks that work with vi.spyOn(Storage.prototype, ...) in tests.
// We define methods on Storage.prototype and create objects that inherit from it.
const createStorageMock = (): Storage => {
  const store: Record<string, string> = {}
  
  const storage = Object.create(Storage.prototype, {
    length: {
      get() {
        return Object.keys(store).length
      },
    },
  })
  
  // Store the data object on the instance for the prototype methods to access
  storage._store = store
  
  return storage
}

// Define the actual implementations on Storage.prototype so spies work
Storage.prototype.getItem = function(key: string): string | null {
  return this._store?.[key] ?? null
}

Storage.prototype.setItem = function(key: string, value: string): void {
  if (this._store) this._store[key] = value
}

Storage.prototype.removeItem = function(key: string): void {
  if (this._store) delete this._store[key]
}

Storage.prototype.clear = function(): void {
  if (this._store) {
    Object.keys(this._store).forEach(key => delete this._store[key])
  }
}

Storage.prototype.key = function(index: number): string | null {
  return this._store ? Object.keys(this._store)[index] ?? null : null
}

// Mock localStorage and sessionStorage to ensure consistent behavior across all
// Node.js environments (some may have broken storage due to --localstorage-file flag)
Object.defineProperty(window, 'localStorage', {
  value: createStorageMock(),
  writable: true,
})

Object.defineProperty(window, 'sessionStorage', {
  value: createStorageMock(),
  writable: true,
})

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation(query => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(), // deprecated
    removeListener: vi.fn(), // deprecated
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
})