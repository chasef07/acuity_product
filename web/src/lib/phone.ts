export function hasE164DigitCount(value: string) {
  const digits = value.replaceAll(/\D/g, "")
  return digits.length >= 8 && digits.length <= 15
}
