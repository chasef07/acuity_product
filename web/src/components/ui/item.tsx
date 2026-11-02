import { mergeProps } from "@base-ui/react/merge-props"
import { useRender } from "@base-ui/react/use-render"

import { cn } from "@/lib/utils"

function Item({
  className,
  render,
  ...props
}: useRender.ComponentProps<"div">) {
  return useRender({
    defaultTagName: "div",
    props: mergeProps<"div">(
      {
        className: cn(
          "group/item flex w-full flex-wrap items-center gap-2.5 rounded-md border border-transparent px-3 py-2.5 text-xs/relaxed transition-colors duration-100 outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 [a]:transition-colors [a]:hover:bg-muted",
          className
        ),
      },
      props
    ),
    render,
    state: {
      slot: "item",
    },
  })
}

export { Item }
