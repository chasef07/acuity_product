export function isCompletePhoneSearch(value: string) {
  const digits = value.replaceAll(/\D/g, "")
  return digits.length === 10 || (digits.length === 11 && digits.startsWith("1"))
}
