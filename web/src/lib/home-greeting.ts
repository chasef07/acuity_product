import { ordinalDay } from "./format-time.ts"

const greetings = ["Good Morning", "Howdy", "Namaste", "Hello", "Welcome back", "Bonjour", "Hola", "Ciao", "Aloha", "Good to see you"]
const dayFormatter = new Intl.DateTimeFormat("en-CA", { timeZone: "America/New_York", year: "numeric", month: "2-digit", day: "2-digit" })
const dateFormatter = new Intl.DateTimeFormat("en-US", { timeZone: "UTC", weekday: "long", month: "long", day: "numeric", year: "numeric" })

export function homeDay(now: Date): string {
  const parts = dayFormatter.formatToParts(now)
  return ["year", "month", "day"].map((type) => parts.find((part) => part.type === type)!.value).join("-")
}

export function homeGreeting(day: string) {
  const date = new Date(`${day}T00:00:00Z`)
  const dateLabel = dateFormatter.formatToParts(date).map((part) => part.type === "day" ? ordinalDay(date.getUTCDate()) : part.type === "literal" ? " " : part.value).join("")
  const dayIndex = Math.floor(date.getTime() / 86_400_000)
  return { greeting: greetings[((dayIndex % greetings.length) + greetings.length) % greetings.length], dateLabel }
}
