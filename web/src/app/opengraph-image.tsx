import { ImageResponse } from "next/og"

import { AcuityMark } from "@/components/acuity-mark"

export const alt = "Acuity Health: Voice AI agents for patient access"
export const size = { width: 1200, height: 630 }
export const contentType = "image/png"

export default function OpenGraphImage() {
  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          flexDirection: "column",
          justifyContent: "space-between",
          padding: "56px 64px",
          color: "#151715",
          background: "#f4f2ed",
          fontFamily: "geist",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 14 }}>
          <AcuityMark width={44} height={44} />
          <span style={{ fontSize: 28, letterSpacing: "-1px" }}>Acuity Health</span>
        </div>

        <div style={{ display: "flex", flexDirection: "column" }}>
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              fontSize: 84,
              lineHeight: 1.06,
              letterSpacing: "-4px",
            }}
          >
            <span>Voice AI agents</span>
            <span>for patient access.</span>
          </div>
          <span
            style={{
              marginTop: 28,
              color: "#61655f",
              fontSize: 22,
              letterSpacing: "-0.5px",
            }}
          >
            Patient calls · Insurance eligibility · Appointment booking
          </span>
        </div>

        <div
          style={{
            display: "flex",
            justifyContent: "flex-end",
            color: "#61655f",
            fontSize: 19,
          }}
        >
          acuityhealth.io
        </div>
      </div>
    ),
    size,
  )
}
