// Responsive intent is expressed in class names, and happy-dom evaluates no
// media queries -- so a test reads the classes on the element and its ancestors
// rather than an observed layout. A responsive prefix is the idiom these files
// use, so "max-md:hidden" has to count just as bare "hidden" does.
export const classesOnAndAbove = (el: HTMLElement) => {
  const classes: string[] = [];
  for (let n: HTMLElement | null = el; n; n = n.parentElement)
    classes.push(...n.classList);
  return classes;
};

// A min-width prefix reverses the meaning: "hidden" and "max-md:hidden" hide on
// a phone, but "sm:hidden" hides from sm up, so a phone is exactly where it
// shows. Excluding those prefixes is what lets a phone-only marker be asserted.
const minWidthPrefix = /^(sm|md|lg|xl|2xl):/;

export const hiddenOnPhones = (el: HTMLElement) =>
  classesOnAndAbove(el).some(
    (c) => /(^|:)hidden$/.test(c) && !minWidthPrefix.test(c),
  );

// order-* below md is what let the tab sequence disagree with the reading order
// (WCAG 2.4.3): the reason drew under the controls but was focused before them.
export const reorderedOnPhones = (el: HTMLElement) =>
  classesOnAndAbove(el).some((c) => /(^|:)order-/.test(c));

// basis-full inside a wrapping row is how this codebase starts a new line below
// a breakpoint: the element takes the full line and pushes itself onto its own.
export const breaksLineOnPhones = (el: HTMLElement) =>
  classesOnAndAbove(el).some((c) => /(^|:)basis-full$/.test(c));
