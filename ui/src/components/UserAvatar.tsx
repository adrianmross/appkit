"use client"

import type { Me } from "../lib/types"
import { Avatar, AvatarFallback, AvatarImage } from "./ui/avatar"

export function displayName(me: Me | null) {
  return me?.user?.name || me?.user?.email || me?.token.name || "Account"
}

function initials(name: string) {
  const words = name.replace(/@.*/, "").split(/[\s._-]+/).filter(Boolean)
  return (words.length > 1 ? words[0][0] + words[words.length - 1][0] : name.slice(0, 1)).toUpperCase()
}

export function UserAvatar({ me, size }: { me: Me | null; size?: "default" | "sm" | "lg" }) {
  return (
    <Avatar size={size}>
      {me?.user?.avatar && <AvatarImage src={me.user.avatar} alt="" />}
      <AvatarFallback>{initials(displayName(me))}</AvatarFallback>
    </Avatar>
  )
}

// Downscales a picked image to a square JPEG data URL so the stored avatar
// stays a few KB, well under the server's 256KB cap.
export async function avatarDataURL(file: File, px = 256): Promise<string> {
  const bitmap = await createImageBitmap(file)
  const side = Math.min(bitmap.width, bitmap.height)
  const canvas = document.createElement("canvas")
  canvas.width = canvas.height = px
  canvas
    .getContext("2d")!
    .drawImage(bitmap, (bitmap.width - side) / 2, (bitmap.height - side) / 2, side, side, 0, 0, px, px)
  return canvas.toDataURL("image/jpeg", 0.85)
}
