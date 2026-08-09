import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { Button } from "./button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "./dialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "./tooltip";

/**
 * shadcn is admitted here as **behaviour, not appearance** — so this file tests
 * the behaviour that was the reason for admitting it, and nothing about how any
 * of it looks.
 *
 * Three components and no more: `button`, `dialog`, `tooltip`. The spine and
 * the lanes are hand-written CSS grid, because a component library has nothing
 * to say about a layout organised around a twelve-character string.
 */

describe("the shadcn baseline", () => {
  it("renders a button as another element without losing its styling", async () => {
    render(
      <Button asChild>
        <a href="/lanes">Open the lanes</a>
      </Button>,
    );

    const link = screen.getByRole("link", { name: "Open the lanes" });
    expect(
      link.className,
      "`asChild` is most of why this component is worth vendoring: a link that " +
        "looks like a button has to stay a link, or keyboard and middle-click " +
        "both stop working",
    ).toContain("bg-ink");
  });

  it("closes a dialog on Escape and gives focus back to what opened it", async () => {
    const user = userEvent.setup();
    render(
      <Dialog>
        <DialogTrigger>Inspect the mandate</DialogTrigger>
        <DialogContent>
          <DialogTitle>Checkout Mandate</DialogTitle>
          <DialogDescription>What the merchant saw.</DialogDescription>
        </DialogContent>
      </Dialog>,
    );

    const trigger = screen.getByRole("button", { name: "Inspect the mandate" });
    await user.click(trigger);
    expect(screen.getByRole("dialog", { name: "Checkout Mandate" })).toBeDefined();

    await user.keyboard("{Escape}");
    expect(
      screen.queryByRole("dialog"),
      "dismissal and focus return are the whole reason this is Radix rather " +
        "than a div with a click handler",
    ).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("opens a tooltip with no provider around it", async () => {
    // The trap this closes: Radix's Tooltip.Root needs a Tooltip.Provider above
    // it, and without one the trigger simply never opens. Ours brings its own,
    // so forgetting is not a state a caller can reach.
    const user = userEvent.setup();
    render(
      <Tooltip>
        <TooltipTrigger>cnf</TooltipTrigger>
        <TooltipContent>The key the open mandate endorsed.</TooltipContent>
      </Tooltip>,
    );

    await user.hover(screen.getByRole("button", { name: "cnf" }));
    expect(
      await screen.findByRole("tooltip"),
      "a tooltip that silently never opens is the failure mode this component " +
        "exists to make unreachable",
    ).toBeDefined();
  });
});
