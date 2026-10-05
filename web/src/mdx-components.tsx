import type { MDXComponents } from "mdx/types"

import { ApproachChart, Stat, Stats } from "@/components/marketing/blog/article-blocks"
import styles from "@/components/marketing/blog/blog.module.css"
import { LoopDiagram } from "@/components/marketing/blog/loop-diagram"

const components: MDXComponents = {
  h2: (props) => <h2 className={styles.h2} {...props} />,
  p: (props) => <p className={styles.p} {...props} />,
  a: (props) => <a className={styles.link} {...props} />,
  strong: (props) => <strong className={styles.strong} {...props} />,
  ul: (props) => <ul className={styles.ul} {...props} />,
  ol: (props) => <ol className={styles.ol} {...props} />,
  blockquote: (props) => <blockquote className={styles.blockquote} {...props} />,
  code: (props) => <code className={styles.code} {...props} />,
  ApproachChart,
  LoopDiagram,
  Stat,
  Stats,
}

export function useMDXComponents(): MDXComponents {
  return components
}
