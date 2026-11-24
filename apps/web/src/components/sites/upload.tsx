'use client'
import { siteApi } from '@/lib/sites'

export const UPLOAD_HINT = 'JPG or PNG; large photos are resized for you.'
export const ACCEPT = 'image/jpeg,image/png'
const MAX_SIDE = 2000
const MAX_BYTES = 1_000_000 // the server accepts at most 1 MB

const blobOf = (c: HTMLCanvasElement, q: number) =>
  new Promise<Blob | null>((ok) => c.toBlob(ok, 'image/jpeg', q))

/** Draws the photo on a canvas at most 2000 px on the long edge and exports a JPEG under 1 MB (EXIF rotation applied). */
export async function resizeToJpeg(file: File): Promise<Blob> {
  if (!/^image\/(jpeg|png)$/.test(file.type)) throw new Error('Use a JPG or PNG photo')
  let bmp: ImageBitmap
  try {
    bmp = await createImageBitmap(file, { imageOrientation: 'from-image' })
  } catch {
    throw new Error("That photo couldn't be read. Try another one.")
  }
  let scale = Math.min(1, MAX_SIDE / Math.max(bmp.width, bmp.height))
  for (let round = 0; round < 6; round++) {
    const c = document.createElement('canvas')
    c.width = Math.max(1, Math.round(bmp.width * scale))
    c.height = Math.max(1, Math.round(bmp.height * scale))
    const g = c.getContext('2d')
    if (!g) throw new Error("This browser can't resize photos")
    g.fillStyle = '#fff' // a transparent PNG becomes white, not black
    g.fillRect(0, 0, c.width, c.height)
    g.drawImage(bmp, 0, 0, c.width, c.height)
    for (const q of [0.85, 0.72, 0.6]) {
      const b = await blobOf(c, q)
      if (b && b.size <= MAX_BYTES) {
        bmp.close()
        return b
      }
    }
    scale *= 0.75
  }
  bmp.close()
  throw new Error('That photo is too large to upload. Try a smaller one.')
}

const b64 = (b: Blob) =>
  new Promise<string>((ok, no) => {
    const r = new FileReader()
    r.onload = () => ok(String(r.result).split(',')[1] ?? '')
    r.onerror = () => no(new Error("That photo couldn't be read. Try another one."))
    r.readAsDataURL(b)
  })

export async function uploadImage(ws: string, siteId: string, file: File) {
  const jpg = await resizeToJpeg(file)
  const name = `${file.name.replace(/\.[^.]*$/, '') || 'photo'}.jpg`
  return (await siteApi.imageUpload(ws, siteId, name, await b64(jpg))).image
}
