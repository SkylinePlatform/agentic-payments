import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";

import { cn } from "../../lib/utils";

/**
 * The shadcn/ui button, re-skinned onto the six tokens.
 *
 * Admitted as behaviour rather than appearance: what is worth taking from
 * shadcn here is `asChild`, the disabled semantics and a focus ring that
 * survives keyboard navigation — not a visual identity. Every class below names
 * a token from `src/styles.css`, and `src/architecture.test.ts` fails if one
 * does not.
 *
 * **There is no `seal` or `broken` variant, and that is a design decision
 * rather than an omission.** The specification says those two are the only
 * colour on the page so that their appearance means something: `seal` is
 * *verified* and `broken` is *the spine failing*. A green Approve button would
 * be claiming verification before anything had been verified, and a red Cancel
 * would spend the one colour that has to survive for a rejection. Actions are
 * `ink`; meaning is `seal` and `broken`.
 */
const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-sm " +
    "font-sans font-medium transition-colors " +
    "disabled:pointer-events-none disabled:opacity-50 " +
    "[&_svg]:pointer-events-none [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        solid: "bg-ink text-paper hover:bg-ink/90",
        outline: "border border-graphite text-ink hover:bg-wash",
        ghost: "text-ink hover:bg-wash",
      },
      size: {
        sm: "h-8 px-3 text-sm",
        md: "h-9 px-4 text-sm",
        lg: "h-10 px-6 text-base",
        icon: "size-9",
      },
    },
    defaultVariants: {
      variant: "solid",
      size: "md",
    },
  },
);

function Button({
  className,
  variant,
  size,
  asChild = false,
  ...props
}: React.ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & {
    /** Render the child element instead of a `<button>`, keeping the styling. */
    asChild?: boolean;
  }) {
  const Comp = asChild ? Slot : "button";
  return (
    <Comp
      data-slot="button"
      className={cn(buttonVariants({ variant, size }), className)}
      {...props}
    />
  );
}

export { Button, buttonVariants };
