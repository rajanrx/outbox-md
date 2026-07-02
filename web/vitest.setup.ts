// Guarantee a working localStorage for every test, regardless of environment.
// The node env has none, and jsdom's Web Storage is not reliably present across
// Node/jsdom versions — so tests that persist view state (useDiffView) fail on
// some machines with "localStorage is undefined". An in-memory shim, installed
// only when the runtime lacks one, makes those tests portable. Registered as a
// vitest setupFile (runs once per test file, in that file's environment).
if (typeof globalThis.localStorage === "undefined") {
  const store = new Map<string, string>();
  const shim: Storage = {
    getItem: (k) => (store.has(k) ? store.get(k)! : null),
    setItem: (k, v) => {
      store.set(k, String(v));
    },
    removeItem: (k) => {
      store.delete(k);
    },
    clear: () => {
      store.clear();
    },
    key: (i) => Array.from(store.keys())[i] ?? null,
    get length() {
      return store.size;
    },
  };
  Object.defineProperty(globalThis, "localStorage", { value: shim, configurable: true });
}
