export const PASSWORD_MIN_LENGTH = 12
export const PASSWORD_MAX_LENGTH = 128

export function confirmedPassword(data: FormData): string | undefined {
  const password = String(data.get("password") ?? "")
  return password === String(data.get("confirmation") ?? "")
    ? password
    : undefined
}
