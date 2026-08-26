import { readFile } from "node:fs/promises"
import { join } from "node:path"

import { ImageResponse } from "next/og"

export const alt = "Acuity Health: AI agents for medical enterprises"
export const size = { width: 1200, height: 630 }
export const contentType = "image/png"

const logoData = await readFile(
  join(process.cwd(), "src/app/acuity-health-mark-og.png"),
  "base64",
)
const logoSrc = `data:image/png;base64,${logoData}`

export default function OpenGraphImage() {
  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          position: "relative",
          padding: "62px 70px",
          color: "#101820",
          background:
            "linear-gradient(135deg, #f8faf8 0%, #dce8e1 54%, #c7d7ce 100%)",
          fontFamily: "Georgia, serif",
        }}
      >
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            justifyContent: "space-between",
            width: "100%",
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: 22 }}>
            <img alt="" height="108" src={logoSrc} width="108" />
            <span style={{ fontFamily: "Arial, sans-serif", fontSize: 34, fontWeight: 700 }}>
              Acuity Health
            </span>
          </div>

          <div style={{ display: "flex", alignItems: "flex-end", justifyContent: "space-between" }}>
            <div style={{ display: "flex", flexDirection: "column", maxWidth: 780 }}>
              <span style={{ fontSize: 86, lineHeight: 0.95, letterSpacing: "-4px" }}>
                Redesign patient access.
              </span>
            </div>
            <span
              style={{
                maxWidth: 270,
                paddingBottom: 8,
                fontFamily: "Arial, sans-serif",
                fontSize: 22,
                lineHeight: 1.35,
                letterSpacing: "1px",
                textTransform: "uppercase",
              }}
            >
              AI agents for medical enterprises
            </span>
          </div>
        </div>
      </div>
    ),
    size,
  )
}
