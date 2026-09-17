import type { SVGProps } from 'react'
import type { WorkspaceView } from '../workspaceTypes'

const base = {
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.6,
  strokeLinecap: 'round' as const,
  strokeLinejoin: 'round' as const,
  'aria-hidden': true,
}

type IconProps = SVGProps<SVGSVGElement> & { size?: number }

export function NavIcon({ view, size = 18, ...rest }: IconProps & { view: WorkspaceView }) {
  const common = { ...base, width: size, height: size, ...rest }
  switch (view) {
    case 'now':
      return (
        <svg {...common}>
          <path d="M3.5 10.5 12 3l8.5 7.5" />
          <path d="M5.5 9.5V21h13V9.5M9.5 21v-6h5v6" />
        </svg>
      )
    case 'conversations':
      return (
        <svg {...common}>
          <path d="M4 5.5h16v11H9l-5 4v-15Z" />
          <path d="M8 9h8M8 13h5" />
        </svg>
      )
    case 'memories':
      return (
        <svg {...common}>
          <path d="M12 20.5c4.2-2.7 7-6 7-10.2A4.8 4.8 0 0 0 14.2 5c-1 0-1.8.3-2.2 1-.4-.7-1.2-1-2.2-1A4.8 4.8 0 0 0 5 10.3c0 4.2 2.8 7.5 7 10.2Z" />
        </svg>
      )
    case 'watches':
      return (
        <svg {...common}>
          <path d="M2.8 12s3.4-5.5 9.2-5.5 9.2 5.5 9.2 5.5-3.4 5.5-9.2 5.5S2.8 12 2.8 12Z" />
          <circle cx="12" cy="12" r="2.4" />
        </svg>
      )
    default:
      return (
        <svg {...common}>
          <path d="M7 3.5v3M17 3.5v3M4 9h16" />
          <rect x="4" y="5.5" width="16" height="15" rx="2" />
          <path d="m8.5 14 2 2 4.5-5" />
        </svg>
      )
  }
}

export function ArrowIcon({ size = 16, ...rest }: IconProps) {
  return (
    <svg {...base} width={size} height={size} {...rest}>
      <path d="M5 12h14m-5-5 5 5-5 5" />
    </svg>
  )
}

export function BackIcon({ size = 16, ...rest }: IconProps) {
  return (
    <svg {...base} width={size} height={size} {...rest}>
      <path d="M19 12H5m5 5-5-5 5-5" />
    </svg>
  )
}

export function CloseIcon({ size = 16, ...rest }: IconProps) {
  return (
    <svg {...base} width={size} height={size} {...rest}>
      <path d="M6 6l12 12M18 6 6 18" />
    </svg>
  )
}

export function PlusIcon({ size = 16, ...rest }: IconProps) {
  return (
    <svg {...base} width={size} height={size} {...rest}>
      <path d="M12 5v14M5 12h14" />
    </svg>
  )
}

export function SearchIcon({ size = 16, ...rest }: IconProps) {
  return (
    <svg {...base} width={size} height={size} {...rest}>
      <circle cx="11" cy="11" r="6.5" />
      <path d="m20 20-4-4" />
    </svg>
  )
}

export function ChevronIcon({ size = 16, ...rest }: IconProps) {
  return (
    <svg {...base} width={size} height={size} {...rest}>
      <path d="m6 9 6 6 6-6" />
    </svg>
  )
}

export function RefreshIcon({ size = 16, ...rest }: IconProps) {
  return (
    <svg {...base} width={size} height={size} {...rest}>
      <path d="M20 12a8 8 0 1 1-2.3-5.6" />
      <path d="M20 4v5h-5" />
    </svg>
  )
}
