export const liveKitCaseStudyUrl =
  "https://livekit.com/blog/how-acuity-health-transforms-patient-access-with-livekit-voice-agents"
export const modMedListingUrl =
  "https://modmed.my.site.com/synapsysmarketplace/s/partner-app/a9YUW000000jfGj2AI/data-buddies-solutions-llc"

export type StoryArt = "wave" | "calendar" | "network" | "rings" | "bars" | "slots"

export type Story = {
  title: string
  partner: string
  href: string
  external: boolean
  art: StoryArt
  tone: "dark" | "light"
}

export const stories: Story[] = [
  {
    title: "80% of calls fully contained by AI agents",
    partner: "LiveKit",
    href: liveKitCaseStudyUrl,
    external: true,
    art: "wave",
    tone: "dark",
  },
  {
    title: "Patient calls booked straight into AdvancedMD",
    partner: "AdvancedMD",
    href: "/integrations/advancedmd",
    external: false,
    art: "calendar",
    tone: "light",
  },
  {
    title: "Available on the ModMed SynapSYS Marketplace",
    partner: "ModMed",
    href: modMedListingUrl,
    external: true,
    art: "network",
    tone: "dark",
  },
  {
    title: "Bringing AI agents to eye care practices",
    partner: "Whitecotton Vision",
    href: "https://whitecottonvision.com/acuity-health",
    external: true,
    art: "rings",
    tone: "dark",
  },
]
