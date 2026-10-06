import clsx from "clsx";
import { Icon } from "./icon";
import { useState } from "react";
import { fileKind, FileKind } from "../lib/format";
import { useApp } from "../store";

const icons: Record<FileKind | "dir", string> = {
  dir: "folder",
  image: "image",
  video: "movie",
  audio: "music_note",
  text: "description",
  pdf: "picture_as_pdf",
  archive: "folder_zip",
  other: "insert_drive_file",
};

const tints: Record<FileKind | "dir", string> = {
  dir: "text-hl",
  image: "text-info",
  video: "text-danger",
  audio: "text-ok",
  text: "text-mute",
  pdf: "text-danger",
  archive: "text-warn",
  other: "text-faint",
};

/**
 * A 28px square: the server-made thumbnail for images and videos, or a
 * type icon. Thumbnails go through the local proxy so the API key applies.
 */
export function FileThumb({ name, mime = "", id, fsPath, dir }: { name: string; mime?: string; id?: string; fsPath?: string; dir?: boolean }) {
  const base = useApp((s) => s.app?.mediaBase ?? "");
  const kind: FileKind | "dir" = dir ? "dir" : fileKind(name, mime);
  const [failed, setFailed] = useState(false);
  const thumbable = (kind === "image" || kind === "video") && base && !failed;
  if (thumbable) {
    const src = id ? `${base}/thumb/${id}?s=64` : `${base}/fsthumb?s=64&p=${encodeURIComponent(fsPath ?? "")}`;
    return (
      <img
        src={src}
        alt=""
        loading="lazy"
        draggable={false}
        onError={() => setFailed(true)}
        className="h-7 w-7 shrink-0 rounded-sm bg-raised object-cover"
      />
    );
  }
  return (
    <span className={clsx("flex h-7 w-7 shrink-0 items-center justify-center rounded-sm bg-raised/70", tints[kind])}>
      <Icon name={icons[kind]} />
    </span>
  );
}

export function NameCell({ name, sub, thumb }: { name: string; sub?: string; thumb: React.ReactNode }) {
  return (
    <div className="flex min-w-0 items-center gap-2.5">
      {thumb}
      <div className="min-w-0">
        <div className="truncate" title={name}>
          {name}
        </div>
        {sub && <div className="truncate text-xs text-faint">{sub}</div>}
      </div>
    </div>
  );
}
