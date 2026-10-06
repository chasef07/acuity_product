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

export function formatUSPhone(phone: string) {
  const match = phone.match(/^\+1(\d{3})(\d{3})(\d{4})$/)
  if (!match) return phone
  return `(${match[1]}) ${match[2]}-${match[3]}`
}

export function formatUSPhoneDigits(value: string) {
  const digits = value.replace(/\D/g, "")
  const local = digits.length === 11 && digits.startsWith("1") ? digits.slice(1) : digits
  if (local.length !== 10) return value
  return `(${local.slice(0, 3)}) ${local.slice(3, 6)}-${local.slice(6)}`
}
