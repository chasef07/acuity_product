import type { MDXComponents } from "mdx/types"
import Link from "next/link"
import type { ComponentPropsWithoutRef } from "react"

import {
  ApproachChart,
  PullQuote,
  Stat,
  Stats,
  Step,
  Steps,
} from "@/components/marketing/blog/article-blocks"
import styles from "@/components/marketing/blog/blog.module.css"
import { LoopDiagram } from "@/components/marketing/blog/loop-diagram"

function Anchor({ href = "", children, ...rest }: ComponentPropsWithoutRef<"a">) {
  if (href.startsWith("/")) {
    return (
      <Link className={styles.link} href={href} {...rest}>
        {children}
      </Link>
    )
  }
  return (
    <a className={styles.link} href={href} rel="noreferrer" target="_blank" {...rest}>
      {children}
    </a>
  )
}

const components: MDXComponents = {
  h2: (props) => <h2 className={styles.h2} {...props} />,
  h3: (props) => <h3 className={styles.h3} {...props} />,
  p: (props) => <p className={styles.p} {...props} />,
  a: Anchor,
  strong: (props) => <strong className={styles.strong} {...props} />,
  ul: (props) => <ul className={styles.ul} {...props} />,
  ol: (props) => <ol className={styles.ol} {...props} />,
  blockquote: (props) => <blockquote className={styles.blockquote} {...props} />,
  code: (props) => <code className={styles.code} {...props} />,
  pre: (props) => <pre className={styles.pre} {...props} />,
  table: (props) => (
    <div className={styles.tableWrap}>
      <table className={styles.table} {...props} />
    </div>
  ),
  ApproachChart,
  LoopDiagram,
  PullQuote,
  Stat,
  Stats,
  Step,
  Steps,
}

export function useMDXComponents(): MDXComponents {
  return components
}
