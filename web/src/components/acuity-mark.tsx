import type { ComponentProps } from "react"

import { acuityMarkDots } from "@/lib/acuity-mark-motion"

function AcuityMark(props: ComponentProps<"svg">) {
  return (
    <svg
      viewBox="0 0 32 32"
      fill="currentColor"
      aria-hidden="true"
      focusable="false"
      {...props}
    >
      {acuityMarkDots.map(([x, y]) => (
        <circle key={`${x},${y}`} cx={x} cy={y} r="2.75" />
      ))}
    </svg>
  )
}

export { AcuityMark }
