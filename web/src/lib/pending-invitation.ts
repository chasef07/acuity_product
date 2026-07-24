"use client"

const storageKey = "acuity.pendingInvitation"

export function capturePendingInvitation(): string | undefined {
  const token = window.location.hash.slice(1).trim()
  if (token.length < 32 || token.length > 256) {
    return pendingInvitation()
  }
  window.localStorage.setItem(storageKey, token)
  window.history.replaceState(null, "", window.location.pathname)
  return token
}

export function pendingInvitation(): string | undefined {
  return window.localStorage.getItem(storageKey) ?? undefined
}

export function clearPendingInvitation() {
  window.localStorage.removeItem(storageKey)
}
