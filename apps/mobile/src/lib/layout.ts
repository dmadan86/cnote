/** Width (dp) at which phone push-navigation becomes a two-pane list | detail split (iPad, Android tablets). */
export const SPLIT_MIN_WIDTH = 768

export const isSplit = (width: number) => width >= SPLIT_MIN_WIDTH

/** List pane width in split mode: a third of the screen, clamped so neither pane gets cramped. */
export const listPaneWidth = (width: number) => Math.min(420, Math.max(300, Math.round(width / 3)))
