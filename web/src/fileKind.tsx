import {
  File as FileIcon,
  FileArchive,
  FileAudio,
  FileCode,
  FileImage,
  FileSpreadsheet,
  FileText,
  FileVideo,
  Presentation,
} from 'lucide-react';

// Files without a picture show an icon and colour for their kind, and the
// extension on larger tiles, so a folder of mixed files is easy to scan.
const kinds: [string, string][] = [
  ['pdf', 'pdf'],
  ['doc', 'doc docx odt rtf pages'],
  ['sheet', 'xls xlsx xlsm ods csv tsv numbers'],
  ['slides', 'ppt pptx odp key'],
  ['archive', 'zip rar 7z tar gz tgz bz2 xz zst iso dmg'],
  ['audio', 'mp3 wav flac m4a aac ogg oga opus wma aiff'],
  ['video', 'mp4 m4v mov webm mkv avi wmv mpg mpeg'],
  ['image', 'png jpg jpeg gif webp avif svg heic heif bmp tif tiff ico psd'],
  [
    'code',
    'json yaml yml toml xml html htm css js jsx mjs cjs ts tsx go py rs c h cc cpp hpp cs java kt swift rb php lua sql sh bash zsh ps1 diff patch ini conf env',
  ],
  ['text', 'txt md markdown log'],
];
const byExtension = new Map(
  kinds.flatMap(([kind, list]) => list.split(' ').map((ext) => [ext, kind] as const)),
);

export function extension(name: string) {
  const dot = name.lastIndexOf('.');
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : '';
}

export function fileKind(name: string) {
  return byExtension.get(extension(name)) ?? 'other';
}

const icons: Record<string, typeof FileIcon> = {
  pdf: FileText,
  doc: FileText,
  sheet: FileSpreadsheet,
  slides: Presentation,
  archive: FileArchive,
  audio: FileAudio,
  video: FileVideo,
  image: FileImage,
  code: FileCode,
  text: FileText,
};

export default function FileKindIcon({ name }: { name: string }) {
  const kind = fileKind(name);
  const Icon = icons[kind] ?? FileIcon;
  const ext = extension(name);
  return (
    <span className={'file-icon kind-' + kind}>
      <Icon size={19} />
      {ext && ext.length <= 5 && <small className="file-ext">{ext}</small>}
    </span>
  );
}
