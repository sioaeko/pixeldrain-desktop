import { useEffect, useState } from "react";

/** useState that survives restarts (localStorage), for view preferences. */
export function usePref<T>(key: string, initial: T): [T, (v: T) => void] {
  const [value, setValue] = useState<T>(() => {
    try {
      const raw = localStorage.getItem(`pd:${key}`);
      return raw ? (JSON.parse(raw) as T) : initial;
    } catch {
      return initial;
    }
  });
  useEffect(() => {
    try {
      localStorage.setItem(`pd:${key}`, JSON.stringify(value));
    } catch {
      // storage full or unavailable: the preference just won't persist
    }
  }, [key, value]);
  return [value, setValue];
}
