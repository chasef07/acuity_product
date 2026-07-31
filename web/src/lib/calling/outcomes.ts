export function providerOutcomeLabel(value: string) {
  switch (value) {
    case "NO_ANSWER":
      return "No answer"
    case "BUSY":
      return "Busy"
    case "DECLINED":
      return "Declined"
    case "STATUS_UNKNOWN":
      return "Status unknown"
    case "MEDIA_READINESS_FAILED":
    case "MEDIA_FAILURE":
      return "Media failure"
    case "FAILED":
      return "Failed"
    default:
      return value.toLowerCase().replaceAll("_", " ")
  }
}
