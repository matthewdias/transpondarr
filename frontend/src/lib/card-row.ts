// rounded-none and border-0 undo the Item primitive's own radius and all-round border.
const divided = "rounded-none border-0 border-b border-border last:border-b-0";

// A divided row that runs to its card's edges (#331).
export const cardRow = `${divided} px-4 py-3`;

// For a row that a 32px button or the 48px poster already makes tall, where py-3 would only add height.
export const cardRowDense = `${divided} px-4 py-2`;
