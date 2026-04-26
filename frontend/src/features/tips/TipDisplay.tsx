import type { Tip } from './tipTypes'

type TipDisplayProps = {
  tip?: Tip
}

export function TipDisplay({ tip }: TipDisplayProps) {
  return <div>{tip?.text ?? 'Tip placeholder'}</div>
}
