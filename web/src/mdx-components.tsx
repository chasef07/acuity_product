import type { MDXComponents } from "mdx/types"

import { ApproachChart, Stat, Stats } from "@/components/marketing/blog/article-blocks"
import styles from "@/components/marketing/blog/blog.module.css"
import { ContextCopy } from "@/components/marketing/blog/context-copy"
import { DimensionMap } from "@/components/marketing/blog/dimension-map"
import { EmbedProject } from "@/components/marketing/blog/embed-project"
import { LetterShift, TokenFlow } from "@/components/marketing/blog/letter-handles"
import { LoopDiagram } from "@/components/marketing/blog/loop-diagram"
import { RefGenerations } from "@/components/marketing/blog/ref-generations"
import { SimilarKeys } from "@/components/marketing/blog/similar-keys"

const components: MDXComponents = {
  h2: (props) => <h2 className={styles.h2} {...props} />,
  p: (props) => <p className={styles.p} {...props} />,
  a: (props) => <a className={styles.link} {...props} />,
  strong: (props) => <strong className={styles.strong} {...props} />,
  ul: (props) => <ul className={styles.ul} {...props} />,
  ol: (props) => <ol className={styles.ol} {...props} />,
  blockquote: (props) => <blockquote className={styles.blockquote} {...props} />,
  code: (props) => <code className={styles.code} {...props} />,
  pre: (props) => <pre className={styles.pre} {...props} />,
  ApproachChart,
  ContextCopy,
  DimensionMap,
  EmbedProject,
  LetterShift,
  LoopDiagram,
  RefGenerations,
  SimilarKeys,
  TokenFlow,
  Stat,
  Stats,
}

export function useMDXComponents(): MDXComponents {
  return components
}
