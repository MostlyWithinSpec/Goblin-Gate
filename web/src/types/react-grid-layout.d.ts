// Minimal types for react-grid-layout 1.4 (no @types package is used).
declare module 'react-grid-layout' {
  import type { ComponentType, ReactNode } from 'react'
  export interface Layout {
    i: string
    x: number
    y: number
    w: number
    h: number
    minW?: number
    minH?: number
    static?: boolean
  }
  export interface ResponsiveProps {
    className?: string
    layouts: Record<string, Layout[]>
    breakpoints: Record<string, number>
    cols: Record<string, number>
    rowHeight?: number
    draggableHandle?: string
    onLayoutChange?: (current: Layout[], all: Record<string, Layout[]>) => void
    children?: ReactNode
  }
  export const Responsive: ComponentType<ResponsiveProps>
  export function WidthProvider<P>(c: ComponentType<P>): ComponentType<P>
}
