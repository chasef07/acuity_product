import { ImageResponse } from "next/og"

import { AcuityMark } from "@/components/acuity-mark"
import { siteSocialImage } from "@/lib/site"

export const alt = siteSocialImage.alt
export const size = { width: 1200, height: 630 }
export const contentType = "image/png"

const rows = Array.from({ length: 14 }, (_, row) => row)
const columns = Array.from({ length: 46 }, (_, column) => column)

export default function OpenGraphImage() {
  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          position: "relative",
          display: "flex",
          flexDirection: "column",
          justifyContent: "space-between",
          padding: "56px 64px",
          color: "#f4f2ed",
          background:
            "radial-gradient(circle at 78% 30%, #5d625a 0%, rgba(93, 98, 90, 0) 45%), radial-gradient(circle at 20% 90%, #3a3d39 0%, rgba(58, 61, 57, 0) 50%), #1a1b19",
        }}
      >
        <div style={{ position: "absolute", top: 0, right: 0, bottom: 0, left: 0, display: "flex", flexDirection: "column" }}>
          {rows.map((row) => (
            <div key={row} style={{ display: "flex", marginTop: row === 0 ? 150 : 0, height: 30 }}>
              {columns.map((column) => {
                const y = 26 * Math.sin(column * 0.28 + row * 0.35) * Math.cos(column * 0.09)
                return (
                  <div
                    key={column}
                    style={{
                      width: 4,
                      height: 4,
                      marginLeft: column === 0 ? 0 : 22,
                      marginTop: 13 + y,
                      borderRadius: 4,
                      background: "#f4f2ed",
                      opacity: 0.08 + row * 0.02,
                    }}
                  />
                )
              })}
            </div>
          ))}
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: 14 }}>
          <AcuityMark width={40} height={40} />
          <span style={{ fontSize: 28, letterSpacing: "-1px" }}>Acuity Health</span>
        </div>

        <div style={{ display: "flex", flexDirection: "column" }}>
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              fontSize: 88,
              fontWeight: 300,
              lineHeight: 1.04,
              letterSpacing: "-4px",
            }}
          >
            <span>Applied AI</span>
            <span>for medical enterprises.</span>
          </div>
          <span style={{ marginTop: 28, color: "#b7bbb3", fontSize: 22, letterSpacing: "-0.5px" }}>
            Acuity Agents · Acuity Systems · Deployment · Strategy
          </span>
        </div>
      </div>
    ),
    size,
  )
}
