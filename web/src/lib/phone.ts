const canonicalUSPhone = /^\+1\d{10}$/

export function normalizeUSPhone(value: string) {
  const input = value.trim()
  if (canonicalUSPhone.test(input)) return input
  if (!/^\+?[\d\s().-]+$/.test(input)) return ""

  const openParentheses = [...input].filter((character) => character === "(")
  const closeParentheses = [...input].filter((character) => character === ")")
  if (
    openParentheses.length !== closeParentheses.length ||
    openParentheses.length > 1 ||
    input.indexOf(")") < input.indexOf("(")
  ) {
    return ""
  }

  const digits = input.replace(/\D/g, "")
  if (digits.length === 10) return `+1${digits}`
  if (digits.length === 11 && digits.startsWith("1")) return `+${digits}`
  return ""
}
