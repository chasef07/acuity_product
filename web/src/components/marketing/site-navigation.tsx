"use client"

import Link from "next/link"
import { ChevronDown, Menu, X } from "lucide-react"
import { useEffect, useRef, useState, type PointerEvent } from "react"

import { AcuityMark } from "@/components/acuity-mark"
import { PortalSignInTrigger } from "@/components/auth/sign-in-dialog"

import styles from "./enterprise-site.module.css"

type NavigationLink = { href: string; label: string; description: string }

type NavigationGroup = { label: string; links: NavigationLink[] }

type NavigationItem = NavigationGroup | { label: string; href: string }

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
        href: "/method",
        label: "Deployment Strategy",
        description: "Prove it in the operation, then scale.",
      },
      {
        href: "/platform/systems",
        label: "Acuity Systems",
        description: "The staff workspace behind every agent.",
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

function groupId(label: string) {
  return `nav-${label.toLowerCase()}`
}

export function SiteNavigation({ current }: { current: string }) {
  const [openGroup, setOpenGroup] = useState<string | null>(null)
  const [mobileOpen, setMobileOpen] = useState(false)
  const [mobileGroup, setMobileGroup] = useState<string | null>(null)
  const [scrolled, setScrolled] = useState(false)
  const navRef = useRef<HTMLElement>(null)
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
      if (!navRef.current?.contains(event.target as Node)) setOpenGroup(null)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return
      if (navRef.current?.contains(document.activeElement)) {
        document.getElementById(`${groupId(openGroup)}-trigger`)?.focus()
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

  const closeAll = () => {
    setOpenGroup(null)
    setMobileOpen(false)
  }

  const hoverOpen = (label: string) => (event: PointerEvent) => {
    if (event.pointerType === "mouse") setOpenGroup(label)
  }

  const hoverClose = (event: PointerEvent) => {
    if (event.pointerType === "mouse") setOpenGroup(null)
  }

  const toggleGroup = (label: string) => {
    const fromMouse = lastPointer.current === "mouse"
    lastPointer.current = null
    setOpenGroup((open) => (open === label && !fromMouse ? null : label))
  }

  return (
    <header
      className={styles.siteHeader}
      data-scrolled={scrolled || mobileOpen ? "" : undefined}
    >
      <Link className={styles.brand} href="/" aria-label="Acuity Health home" onClick={closeAll}>
        <AcuityMark className={styles.brandMark} />
        <span>Acuity Health</span>
      </Link>

      <nav
        className={styles.primaryNav}
        aria-label="Main navigation"
        ref={navRef}
        onBlur={(event) => {
          if (event.relatedTarget && !navRef.current?.contains(event.relatedTarget)) setOpenGroup(null)
        }}
      >
        <ul className={styles.navList}>
          {navigation.map((item) =>
            "href" in item ? (
              <li key={item.label}>
                <Link
                  className={styles.navLink}
                  href={item.href}
                  aria-current={current === item.href ? "page" : undefined}
                >
                  {item.label}
                </Link>
              </li>
            ) : (
              <li
                className={styles.navGroup}
                key={item.label}
                onPointerEnter={hoverOpen(item.label)}
                onPointerLeave={hoverClose}
              >
                <button
                  type="button"
                  className={styles.navLink}
                  id={`${groupId(item.label)}-trigger`}
                  aria-expanded={openGroup === item.label}
                  aria-controls={groupId(item.label)}
                  data-active={item.links.some((link) => link.href === current) ? "" : undefined}
                  onPointerDown={(event) => {
                    lastPointer.current = event.pointerType
                  }}
                  onClick={() => toggleGroup(item.label)}
                >
                  {item.label}
                  <ChevronDown size={14} aria-hidden="true" />
                </button>
                <div
                  className={styles.navPanel}
                  id={groupId(item.label)}
                  hidden={openGroup !== item.label}
                >
                  <div className={styles.navPanelCard}>
                    <ul>
                      {item.links.map((link) => (
                        <li key={link.href}>
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
                  </div>
                </div>
              </li>
            ),
          )}
        </ul>
      </nav>

      <div className={styles.headerActions}>
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
          onClick={() => setMobileOpen((open) => !open)}
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
                    aria-controls={`mobile-${groupId(item.label)}`}
                    onClick={() =>
                      setMobileGroup((open) => (open === item.label ? null : item.label))
                    }
                  >
                    {item.label}
                    <ChevronDown size={18} aria-hidden="true" />
                  </button>
                  <ul id={`mobile-${groupId(item.label)}`} hidden={mobileGroup !== item.label}>
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
