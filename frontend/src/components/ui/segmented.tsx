"use client";

import * as React from "react";
import { RadioGroup as RadioGroupPrimitive } from "radix-ui";

import { cn } from "@/lib/utils";

// The Calendar view switcher's look: one bar, the chosen item raised.
function Segmented({
  className,
  ...props
}: React.ComponentProps<typeof RadioGroupPrimitive.Root>) {
  return (
    <RadioGroupPrimitive.Root
      data-slot="segmented"
      className={cn(
        "inline-flex w-fit items-center gap-0.5 rounded-lg bg-muted p-[3px]",
        className,
      )}
      {...props}
    />
  );
}

// A radio rather than a toggle, so an arrow key picks the item it moves to --
// which is what a screen reader announcing "radio" tells the user to expect.
function SegmentedItem({
  className,
  ...props
}: React.ComponentProps<typeof RadioGroupPrimitive.Item>) {
  return (
    <RadioGroupPrimitive.Item
      data-slot="segmented-item"
      className={cn(
        "inline-flex size-7 items-center justify-center rounded-md border border-transparent text-foreground/60 transition-[color,box-shadow] outline-none hover:text-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/75 disabled:pointer-events-none disabled:opacity-50 data-[state=checked]:bg-background data-[state=checked]:text-foreground data-[state=checked]:shadow-sm dark:text-muted-foreground dark:hover:text-foreground dark:focus-visible:ring-ring/50 dark:data-[state=checked]:border-border dark:data-[state=checked]:bg-input/10 dark:data-[state=checked]:text-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        className,
      )}
      {...props}
    />
  );
}

export { Segmented, SegmentedItem };
