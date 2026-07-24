import { createAuth } from "@/lib/auth-factory"

type Auth = ReturnType<typeof createAuth>

let auth: Auth | undefined

export function getAuth(): Auth {
  auth ??= createAuth()
  return auth
}
