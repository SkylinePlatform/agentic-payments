import * as React from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";

import { cn } from "../../lib/utils";

/**
 * The shadcn/ui dialog over Radix, re-skinned onto the six tokens.
 *
 * What is being adopted is dismissal and focus management — Escape, the focus
 * trap, returning focus to the trigger, `aria-modal` and the labelling — none
 * of which is worth reimplementing and all of which is easy to get subtly
 * wrong. The appearance is ours.
 *
 * Two departures from the upstream component, both deliberate:
 *
 * - **No enter or exit animation.** Upstream leans on `tw-animate-css`
 *   utilities; the stills are this project's deliverable and a dependency
 *   whose entire job is motion earns nothing here.
 * - **The scrim is `bg-paper`, not a black wash.** A dark scrim is a dark scrim
 *   only in the light theme; in the dark one it would lighten the page, because
 *   the token that means *ground* is what changes between themes and the token
 *   that means *text* is not it. `paper` at 85% pushes the page toward whichever
 *   ground it is currently on, which is what "recede" means in both.
 */
const Dialog = DialogPrimitive.Root;
const DialogTrigger = DialogPrimitive.Trigger;
const DialogPortal = DialogPrimitive.Portal;
const DialogClose = DialogPrimitive.Close;

function DialogOverlay({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Overlay>) {
  return (
    <DialogPrimitive.Overlay
      data-slot="dialog-overlay"
      className={cn("fixed inset-0 z-50 bg-paper/85", className)}
      {...props}
    />
  );
}

function DialogContent({
  className,
  children,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Content>) {
  return (
    <DialogPortal>
      <DialogOverlay />
      <DialogPrimitive.Content
        data-slot="dialog-content"
        className={cn(
          "fixed top-1/2 left-1/2 z-50 grid w-full max-w-lg -translate-x-1/2 -translate-y-1/2",
          "gap-4 rounded-sm border border-graphite bg-wash p-6 text-ink",
          className,
        )}
        {...props}
      >
        {children}
        <DialogPrimitive.Close
          className="absolute top-4 right-4 rounded-sm text-graphite transition-colors hover:text-ink"
          // The icon is decorative; this is the accessible name.
          aria-label="Close"
        >
          <svg
            aria-hidden="true"
            viewBox="0 0 16 16"
            className="size-4 stroke-current"
            fill="none"
            strokeWidth="1.5"
            strokeLinecap="round"
          >
            <path d="M3.5 3.5l9 9M12.5 3.5l-9 9" />
          </svg>
        </DialogPrimitive.Close>
      </DialogPrimitive.Content>
    </DialogPortal>
  );
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-header"
      className={cn("flex flex-col gap-1.5", className)}
      {...props}
    />
  );
}

function DialogFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn("flex flex-col-reverse gap-2 sm:flex-row sm:justify-end", className)}
      {...props}
    />
  );
}

function DialogTitle({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return (
    <DialogPrimitive.Title
      data-slot="dialog-title"
      className={cn("font-display text-lg leading-tight tracking-tight text-ink", className)}
      {...props}
    />
  );
}

function DialogDescription({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Description>) {
  return (
    <DialogPrimitive.Description
      data-slot="dialog-description"
      className={cn("font-sans text-sm text-graphite", className)}
      {...props}
    />
  );
}

export {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogOverlay,
  DialogPortal,
  DialogTitle,
  DialogTrigger,
};
