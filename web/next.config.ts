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
        source: "/specialties/ophthalmology",
        destination: "/ai-receptionist-for-ophthalmology",
        permanent: true,
      },
      {
        source: "/ophthalmology-answering-service",
        destination: "/ai-receptionist-for-ophthalmology",
        permanent: true,
      },
      {
        source: "/after-hours-answering-service-ophthalmology",
        destination: "/ai-receptionist-for-ophthalmology",
        permanent: true,
      },
      {
        source: "/insights/best-ai-answering-service-ophthalmology",
        destination: "/ai-receptionist-for-ophthalmology",
        permanent: true,
      },
      {
        source: "/insights/ai-receptionist-vs-traditional-answering-service",
        destination: "/ai-receptionist-vs-medical-answering-service",
        permanent: true,
      },
      {
        source: "/partners/advancedmd",
        destination: "/advancedmd-ai-receptionist",
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
