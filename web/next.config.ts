import type { NextConfig } from "next"

const nextConfig: NextConfig = {
  output: "standalone",
  experimental: {
    turbopackFileSystemCacheForBuild: true,
  },
  async redirects() {
    return [
      {
        source: "/about",
        destination: "/who-we-are",
        permanent: true,
      },
      {
        source: "/advancedmd-ai-receptionist",
        destination: "/integrations/advancedmd",
        permanent: true,
      },
      {
        source: "/partners/advancedmd",
        destination: "/integrations/advancedmd",
        permanent: true,
      },
    ]
  },
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          {
            key: "Strict-Transport-Security",
            value: "max-age=31536000",
          },
          {
            key: "X-Content-Type-Options",
            value: "nosniff",
          },
          {
            key: "Referrer-Policy",
            value: "strict-origin-when-cross-origin",
          },
          {
            key: "Permissions-Policy",
            value: "camera=(), geolocation=(), payment=()",
          },
        ],
      },
    ]
  },
}

export default nextConfig
