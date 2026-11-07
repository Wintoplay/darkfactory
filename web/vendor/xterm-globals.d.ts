interface XtermTheme {
  background?: string
  foreground?: string
  cursor?: string
  cursorAccent?: string
  selectionBackground?: string
  selectionForeground?: string
  [key: string]: string | undefined
}

interface XtermLinkHandler {
  activate(event: MouseEvent, text: string, range: unknown): void
  hover?(event: MouseEvent, text: string, range: unknown): void
  leave?(event: MouseEvent, text: string, range: unknown): void
}

interface XtermOptions {
  allowProposedApi?: boolean
  convertEol?: boolean
  cursorBlink?: boolean
  cursorStyle?: 'block' | 'underline' | 'bar'
  disableStdin?: boolean
  fontFamily?: string
  fontSize?: number
  lineHeight?: number
  scrollback?: number
  theme?: XtermTheme
  windowOptions?: Record<string, boolean>
  linkHandler?: XtermLinkHandler
}

interface XtermDisposable {
  dispose(): void
}

interface XtermSize {
  cols: number
  rows: number
}

interface XtermModes {
  applicationCursorKeysMode: boolean
  applicationKeypadMode: boolean
  bracketedPasteMode: boolean
  insertMode: boolean
  mouseTrackingMode: 'none' | 'x10' | 'vt200' | 'drag' | 'any'
  originMode: boolean
  reverseWraparoundMode: boolean
  sendFocusMode: boolean
  synchronizedOutputMode: boolean
  wraparoundMode: boolean
}

declare class Terminal {
  constructor(options?: XtermOptions)
  readonly cols: number
  readonly rows: number
  readonly element?: HTMLElement
  readonly modes: XtermModes
  options: XtermOptions
  readonly buffer: {
    active: {
      type: string
      viewportY: number
      baseY: number
      length: number
      getLine(y: number): { translateToString(trimRight?: boolean): string } | undefined
    }
  }
  open(parent: HTMLElement): void
  write(data: string | Uint8Array): void
  reset(): void
  clear(): void
  focus(): void
  refresh(start: number, end: number): void
  scrollLines(amount: number): void
  scrollPages(pages: number): void
  scrollToBottom(): void
  loadAddon(addon: FitAddon): void
  onData(listener: (data: string) => void): XtermDisposable
  onResize(listener: (size: XtermSize) => void): XtermDisposable
  onTitleChange(listener: (title: string) => void): XtermDisposable
  dispose(): void
}

declare class FitAddon {
  fit(): void
  proposeDimensions(): XtermSize | undefined
}
