type CallRecoveryStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">

const callRecoveryStoragePrefix = "acuity.callingRecoveryCall"

export function createBrowserCallRecoveryStore(
  storage: CallRecoveryStorage,
  actorSubject: string,
) {
  const key = `${callRecoveryStoragePrefix}:${encodeURIComponent(actorSubject)}`
  return {
    load: () => storage.getItem(key) || undefined,
    persist(callID: string | undefined) {
      if (callID) storage.setItem(key, callID)
      else storage.removeItem(key)
    },
  }
}
