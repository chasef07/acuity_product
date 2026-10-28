"use client"

import Link from "next/link"
import { ChevronDown, Menu, X } from "lucide-react"
import {
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type PointerEvent,
  type ReactNode,
} from "react"

import { AcuityMark } from "@/components/acuity-mark"
import { PortalSignInTrigger } from "@/components/auth/sign-in-dialog"

import styles from "./enterprise-site.module.css"

type NavigationLink = { href: string; label: string; description: string }

type NavigationGroup = { label: string; links: NavigationLink[] }

type NavigationItem = NavigationGroup | { label: string; href: string }

export type NavigationFeature = {
  href: string
  external: boolean
  title: string
  cta: string
  art: ReactNode
}

const navigation: NavigationItem[] = [
  {
    label: "Platform",
    links: [
      {
        href: "/platform/agents",
        label: "Acuity Agents",
        description: "Voice agents that answer, book, and hand off.",
      },
      {
        href: "/platform/systems",
        label: "Acuity Systems",
        description: "The staff workspace behind every agent.",
      },
      {
        href: "/platform/deployment",
        label: "Deployment",
        description: "On-site with your team until you own it.",
      },
      {
        href: "/platform/strategy",
        label: "Strategy",
        description: "Start with one workflow. Scale on evidence.",
      },
    ],
  },
  {
    label: "Integrations",
    links: [
      {
        href: "/integrations",
        label: "All integrations",
        description: "EHR, PMS, clearinghouse, and voice partners.",
      },
      {
        href: "/integrations/advancedmd",
        label: "AdvancedMD",
        description: "Patient calls become appointments.",
      },
      {
        href: "/integrations/stedi",
        label: "Stedi",
        description: "Healthcare clearinghouse partner.",
      },
    ],
  },
  {
    label: "Company",
    links: [
      {
        href: "/who-we-are",
        label: "Who We Are",
        description: "Founders, clinical leadership, and principles.",
      },
      {
        href: "/security",
        label: "Security",
        description: "Privacy, HIPAA, and service boundaries.",
      },
    ],
  },
  { label: "Blog", href: "/blog" },
]

const groups = navigation.filter((item): item is NavigationGroup => "links" in item)

function triggerId(label: string) {
  return `nav-${label.toLowerCase()}-trigger`
}

export function SiteNavigation({
  current,
  features,
}: {
  current: string
  features: Record<string, NavigationFeature>
}) {
  const [openGroup, setOpenGroup] = useState<string | null>(null)
  const [shownGroup, setShownGroup] = useState(groups[0].label)
  const [panelLeft, setPanelLeft] = useState(0)
  const [mobileOpen, setMobileOpen] = useState(false)
  const [mobileGroup, setMobileGroup] = useState<string | null>(null)
  const [scrolled, setScrolled] = useState(false)
  const headerRef = useRef<HTMLElement>(null)
  const menuButtonRef = useRef<HTMLButtonElement>(null)
  const lastPointer = useRef<string | null>(null)

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 8)
    onScroll()
    window.addEventListener("scroll", onScroll, { passive: true })
    return () => window.removeEventListener("scroll", onScroll)
  }, [])

  useEffect(() => {
    if (!openGroup) return
    const onPointerDown = (event: globalThis.PointerEvent) => {
      if (!headerRef.current?.contains(event.target as Node)) setOpenGroup(null)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return
      if (headerRef.current?.contains(document.activeElement)) {
        document.getElementById(triggerId(openGroup))?.focus()
      }
      setOpenGroup(null)
    }
    document.addEventListener("pointerdown", onPointerDown)
    document.addEventListener("keydown", onKeyDown)
    return () => {
      document.removeEventListener("pointerdown", onPointerDown)
      document.removeEventListener("keydown", onKeyDown)
    }
  }, [openGroup])

  useEffect(() => {
    if (!mobileOpen) return
    const desktop = window.matchMedia("(min-width: 861px)")
    const close = () => setMobileOpen(false)
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return
      menuButtonRef.current?.focus()
      close()
    }
    const onBreakpoint = () => {
      if (desktop.matches) close()
    }
    document.documentElement.style.overflow = "hidden"
    document.addEventListener("keydown", onKeyDown)
    desktop.addEventListener("change", onBreakpoint)
    return () => {
      document.documentElement.style.overflow = ""
      document.removeEventListener("keydown", onKeyDown)
      desktop.removeEventListener("change", onBreakpoint)
    }
  }, [mobileOpen])

  const open = (label: string, from: Element) => {
    setPanelLeft((from.closest("nav") as HTMLElement | null)?.offsetLeft ?? 0)
    setShownGroup(label)
    setOpenGroup(label)
  }

  const closeAll = () => {
    setOpenGroup(null)
    setMobileOpen(false)
  }

  const fromMouse = (action: (target: Element) => void) => (event: PointerEvent) => {
    if (event.pointerType === "mouse") action(event.currentTarget)
  }

  const toggleGroup = (label: string, target: Element) => {
    const mouse = lastPointer.current === "mouse"
    lastPointer.current = null
    if (openGroup === label && !mouse) setOpenGroup(null)
    else open(label, target)
  }

  const shown = groups.find((group) => group.label === shownGroup) ?? groups[0]
  const feature = features[shown.label]

  return (
    <header
      className={styles.siteHeader}
      ref={headerRef}
      data-scrolled={scrolled || mobileOpen ? "" : undefined}
      data-panel-open={openGroup ? "" : undefined}
      onPointerLeave={fromMouse(() => setOpenGroup(null))}
      onBlur={(event) => {
        if (event.relatedTarget && !headerRef.current?.contains(event.relatedTarget)) {
          setOpenGroup(null)
        }
      }}
    >
      <Link className={styles.brand} href="/" aria-label="Acuity Health home" onClick={closeAll}>
        <AcuityMark className={styles.brandMark} />
        <span>Acuity Health</span>
      </Link>

      <nav className={styles.primaryNav} aria-label="Main navigation">
        <ul className={styles.navList}>
          {navigation.map((item) =>
            "href" in item ? (
              <li key={item.label} onPointerEnter={fromMouse(() => setOpenGroup(null))}>
                <Link
                  className={styles.navLink}
                  href={item.href}
                  aria-current={current === item.href ? "page" : undefined}
                >
                  {item.label}
                </Link>
              </li>
            ) : (
              <li key={item.label} onPointerEnter={fromMouse((target) => open(item.label, target))}>
                <button
                  type="button"
                  className={styles.navLink}
                  id={triggerId(item.label)}
                  aria-expanded={openGroup === item.label}
                  aria-controls="nav-panel"
                  data-active={item.links.some((link) => link.href === current) ? "" : undefined}
                  onPointerDown={(event) => {
                    lastPointer.current = event.pointerType
                  }}
                  onClick={(event) => toggleGroup(item.label, event.currentTarget)}
                >
                  {item.label}
                </button>
                {shown.label === item.label ? (
                  <div
                    className={styles.navPanel}
                    id="nav-panel"
                    data-open={openGroup ? "" : undefined}
                    aria-labelledby={triggerId(shown.label)}
                    role="region"
                    style={{ "--nav-left": `${panelLeft}px` } as CSSProperties}
                  >
                    <div className={styles.navPanelInner} key={shown.label}>
                      <ul className={styles.navPanelLinks}>
                        {shown.links.map((link, index) => (
                          <li style={{ "--i": index } as CSSProperties} key={link.href}>
                            <Link
                              className={styles.navPanelLink}
                              href={link.href}
                              aria-current={current === link.href ? "page" : undefined}
                              onClick={closeAll}
                            >
                              <span>{link.label}</span>
                              <small>{link.description}</small>
                            </Link>
                          </li>
                        ))}
                      </ul>
                      {feature ? (
                        <a
                          className={styles.navFeature}
                          style={{ "--i": shown.links.length } as CSSProperties}
                          href={feature.href}
                          target={feature.external ? "_blank" : undefined}
                          rel={feature.external ? "noopener" : undefined}
                          onClick={closeAll}
                        >
                          <div className={styles.navFeatureArt}>{feature.art}</div>
                          <span className={styles.navFeatureTitle}>{feature.title}</span>
                          <span className={styles.navFeatureCta}>{feature.cta}</span>
                        </a>
                      ) : null}
                    </div>
                  </div>
                ) : null}
              </li>
            ),
          )}
        </ul>
      </nav>

      <div className={styles.headerActions} onPointerEnter={fromMouse(() => setOpenGroup(null))}>
        <PortalSignInTrigger className={styles.portalLink} />
        <Link className={styles.workLink} href="/work-with-us">
          Build with us
        </Link>
        <button
          type="button"
          className={styles.menuButton}
          ref={menuButtonRef}
          aria-expanded={mobileOpen}
          aria-controls="mobile-menu"
          aria-label={mobileOpen ? "Close menu" : "Open menu"}
          onClick={() => setMobileOpen((isOpen) => !isOpen)}
        >
          {mobileOpen ? <X size={22} aria-hidden="true" /> : <Menu size={22} aria-hidden="true" />}
        </button>
      </div>

      {mobileOpen ? (
        <nav className={styles.mobileMenu} id="mobile-menu" aria-label="Mobile navigation">
          <ul className={styles.mobileGroups}>
            {navigation.map((item) =>
              "href" in item ? (
                <li key={item.label}>
                  <Link className={styles.mobileGroupButton} href={item.href} onClick={closeAll}>
                    {item.label}
                  </Link>
                </li>
              ) : (
                <li key={item.label}>
                  <button
                    type="button"
                    className={styles.mobileGroupButton}
                    aria-expanded={mobileGroup === item.label}
                    aria-controls={`mobile-${item.label.toLowerCase()}`}
                    onClick={() =>
                      setMobileGroup((isOpen) => (isOpen === item.label ? null : item.label))
                    }
                  >
                    {item.label}
                    <ChevronDown size={18} aria-hidden="true" />
                  </button>
                  <ul id={`mobile-${item.label.toLowerCase()}`} hidden={mobileGroup !== item.label}>
                    {item.links.map((link) => (
                      <li key={link.href}>
                        <Link className={styles.navPanelLink} href={link.href} onClick={closeAll}>
                          <span>{link.label}</span>
                          <small>{link.description}</small>
                        </Link>
                      </li>
                    ))}
                  </ul>
                </li>
              ),
            )}
          </ul>
          <div className={styles.mobileActions}>
            <Link className={styles.darkButton} href="/work-with-us" onClick={closeAll}>
              Build with us
            </Link>
            <PortalSignInTrigger className={styles.mobileSignIn} />
          </div>
        </nav>
      ) : null}
    </header>
  )
}
