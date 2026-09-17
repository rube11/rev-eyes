import './Pills.css'

export type PillOption = {
  value: string
  label: string
  count?: number
}

export function Pills({
  label,
  options,
  value,
  onChange,
  size = 'md',
  scroll = false,
}: {
  label: string
  options: PillOption[]
  value: string
  onChange: (value: string) => void
  size?: 'md' | 'sm'
  scroll?: boolean
}) {
  return (
    <div
      className={`pills pills--${size}${scroll ? ' pills--scroll' : ''}`}
      role="group"
      aria-label={label}
    >
      {options.map((option) => {
        const selected = option.value === value
        return (
          <button
            key={option.value}
            type="button"
            className={`pills__item${selected ? ' is-selected' : ''}`}
            aria-pressed={selected}
            onClick={() => onChange(option.value)}
          >
            {option.label}
            {option.count !== undefined ? (
              <span className="pills__count tnum">{option.count}</span>
            ) : null}
          </button>
        )
      })}
    </div>
  )
}
