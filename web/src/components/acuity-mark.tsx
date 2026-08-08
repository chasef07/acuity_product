import type { ComponentProps } from "react"

function AcuityMark(props: ComponentProps<"svg">) {
  return (
    <svg
      viewBox="0 0 32 32"
      fill="currentColor"
      aria-hidden="true"
      focusable="false"
      {...props}
    >
      <circle cx="16" cy="7" r="2.75" />
      <circle cx="8" cy="11.5" r="2.75" />
      <circle cx="24" cy="11.5" r="2.75" />
      <circle cx="16" cy="16" r="2.75" />
      <circle cx="8" cy="20.5" r="2.75" />
      <circle cx="24" cy="20.5" r="2.75" />
      <circle cx="16" cy="25" r="2.75" />
    </svg>
  )
}

export { AcuityMark }
