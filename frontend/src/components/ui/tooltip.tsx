import * as React from "react";
import * as TooltipPrimitive from "@radix-ui/react-tooltip";

import { cn } from "../../lib/utils";

/**
 * The shadcn/ui tooltip over Radix, re-skinned onto the six tokens.
 *
 * **Radix's `Tooltip.Root` requires a `Tooltip.Provider` above it**, and that is
 * the trap this component exists to close: forget it and the trigger does not
 * open. `Tooltip` below therefore supplies its own provider, so a caller cannot
 * forget one and a test can render a tooltip on its own. Nesting is legal — an
 * app-wide `TooltipProvider` still works — and the innermost provider is the one
 * whose `delayDuration` applies, which is why ours states 0 rather than
 * inheriting Radix's 700ms default.
 *
 * `TooltipBehaviour.test.tsx` is that sentence run: it renders a tooltip with no
 * provider around it and asserts the content appears.
 *
 * The chip is `bg-ink text-paper` — the page inverted, in either theme, because
 * both names keep their meaning when the values swap.
 */
function TooltipProvider({
  delayDuration = 0,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Provider>) {
  return (
    <TooltipPrimitive.Provider
      data-slot="tooltip-provider"
      delayDuration={delayDuration}
      {...props}
    />
  );
}

function Tooltip({ ...props }: React.ComponentProps<typeof TooltipPrimitive.Root>) {
  return (
    <TooltipProvider>
      <TooltipPrimitive.Root data-slot="tooltip" {...props} />
    </TooltipProvider>
  );
}

const TooltipTrigger = TooltipPrimitive.Trigger;

function TooltipContent({
  className,
  sideOffset = 6,
  children,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Content>) {
  return (
    <TooltipPrimitive.Portal>
      <TooltipPrimitive.Content
        data-slot="tooltip-content"
        sideOffset={sideOffset}
        className={cn(
          "z-50 w-fit rounded-sm bg-ink px-2.5 py-1.5",
          "font-sans text-xs text-balance text-paper",
          className,
        )}
        {...props}
      >
        {children}
        <TooltipPrimitive.Arrow className="fill-ink" width={10} height={5} />
      </TooltipPrimitive.Content>
    </TooltipPrimitive.Portal>
  );
}

export { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger };
