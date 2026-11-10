import { HTMLContainer, Rectangle2d, ShapeUtil, T, type TLShape } from 'tldraw'

const TYPE = 'item-embed'

declare module 'tldraw' {
  export interface TLGlobalShapePropsMap {
    [TYPE]: { w: number; h: number; itemKey: string; title: string; status: string; href: string }
  }
}

export type ItemEmbedShape = TLShape<typeof TYPE>

/** Card for a project item: a snapshot of key/title/status at insert time, linking to the item. */
export class ItemEmbedUtil extends ShapeUtil<ItemEmbedShape> {
  static override type = TYPE
  static override props = {
    w: T.number,
    h: T.number,
    itemKey: T.string,
    title: T.string,
    status: T.string,
    href: T.string,
  }

  getDefaultProps(): ItemEmbedShape['props'] {
    return { w: 260, h: 84, itemKey: '', title: '', status: '', href: '' }
  }

  getGeometry(s: ItemEmbedShape) {
    return new Rectangle2d({ width: s.props.w, height: s.props.h, isFilled: true })
  }

  component(s: ItemEmbedShape) {
    return (
      <HTMLContainer
        style={{
          pointerEvents: 'all',
          background: '#fff',
          color: '#111',
          border: '1px solid #ccc',
          borderRadius: 8,
          padding: 10,
          fontSize: 13,
          overflow: 'hidden',
        }}
      >
        <a
          href={s.props.href}
          onPointerDown={(e) => e.stopPropagation()}
          style={{ fontWeight: 600, textDecoration: 'underline' }}
        >
          {s.props.itemKey}
        </a>
        <span style={{ float: 'right', opacity: 0.7 }}>{s.props.status}</span>
        <div style={{ marginTop: 6 }}>{s.props.title}</div>
      </HTMLContainer>
    )
  }

  getIndicatorPath(s: ItemEmbedShape) {
    const p = new Path2D()
    p.rect(0, 0, s.props.w, s.props.h)
    return p
  }
}
