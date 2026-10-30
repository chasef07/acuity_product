const drafts = new Map<string, unknown>()
const listeners = new Set<() => void>()

export function draftScope(...scope: string[]) {
  return scope.join(":")
}

export function draftStore<T>(kind: string) {
  const id = (key: string) => `${kind}:${key}`
  return {
    read: (key: string) => drafts.get(id(key)) as T | undefined,
    write: (key: string, draft: T | undefined) => {
      if (draft) drafts.set(id(key), draft)
      else drafts.delete(id(key))
      changedDrafts()
    },
    subscribe,
  }
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function clearDrafts() {
  drafts.clear()
  changedDrafts()
}

function changedDrafts() {
  if (drafts.size > 0) window.addEventListener("beforeunload", warnUnpublished)
  else window.removeEventListener("beforeunload", warnUnpublished)
  for (const listener of listeners) listener()
}

function warnUnpublished(event: BeforeUnloadEvent) {
  event.preventDefault()
}
