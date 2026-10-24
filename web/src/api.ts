import {
  catalogs,
  getLanguage,
  formatNumber,
  message,
  LocalizedError,
  type MessageDescriptor,
} from './i18n';
export interface User {
  id: string;
  username: string;
  role: 'admin' | 'user';
  quota: number;
  used: number;
  disabled?: boolean;
  email?: string;
  two_factor?: boolean;
  display_name?: string;
  // The picture's version; 0 or missing when there is none.
  avatar?: number;
  remind_expiry?: boolean;
}
export interface Policy {
  registration_open: boolean;
  default_quota: number;
  max_file: number;
  max_share: number;
  default_days: number;
  storage_budget?: number;
  files_per_user?: number;
  files_total?: number;
}
export interface SharedFile {
  id: string;
  name: string;
  size: number;
  offset: number;
  backend: string;
  has_thumb: boolean;
  // The file's folder below a shared folder, such as "Photos/2024".
  folder?: string;
  downloads?: number;
  last_download?: number;
  remaining?: number;
}
export interface Share {
  id: string;
  title: string;
  description: string;
  slug: string;
  expires: number;
  created: number;
  published: boolean;
  protected: boolean;
  files: SharedFile[];
  mode?: 'live' | 'selection';
  folder_id?: string;
  downloads?: number;
  notify?: boolean;
  download_limit?: number;
  last_download?: number;
}
export type Capabilities = Record<
  | 'folders'
  | 'upload'
  | 'share'
  | 'edit_own'
  | 'grant_edit'
  | 'edit_shared'
  | 'add_shared'
  | 'delete_shared',
  boolean
>;
export interface Folder {
  id: string;
  name: string;
  // Empty for a folder at the top of the library.
  parent_id?: string;
}
export interface LibraryFile extends SharedFile {
  folder_id: string;
  share_id: string;
  starred?: boolean;
  received?: boolean;
  received_from?: string;
  received_via?: string;
  auto_title?: boolean;
}
export interface Me {
  user: User;
  policy: Policy;
  email_enabled?: boolean;
}
export class ApiError extends LocalizedError {
  constructor(
    value: string | MessageDescriptor,
    public status: number,
    // The server's stable error code, when it sent one.
    public code = '',
  ) {
    super(value);
  }
}
export async function responseData<T>(response: Response): Promise<T> {
  let data: unknown;
  try {
    if (typeof response.text === 'function') {
      const text = await response.text();
      data = text.trim() === '' ? undefined : JSON.parse(text);
    } else {
      data = await response.json();
    }
  } catch {
    if (!response.ok)
      throw new ApiError(message('errors.http', { status: response.status }), response.status);
    if (
      response.status === 204 ||
      response.status === 205 ||
      response.headers?.get('Content-Length') === '0'
    )
      return undefined as T;
    throw new LocalizedError(message('errors.invalidResponse'));
  }
  if (!response.ok) {
    const failure = data as { error?: unknown; error_code?: unknown } | undefined;
    const key = typeof failure?.error_code === 'string' ? 'errors.' + failure.error_code : '';
    const known =
      key && (Object.hasOwn(catalogs.en, key) || Object.hasOwn(catalogs[getLanguage()] || {}, key));
    throw new ApiError(
      known
        ? message(key)
        : typeof failure?.error === 'string'
          ? failure.error
          : message('errors.http', { status: response.status }),
      response.status,
      typeof failure?.error_code === 'string' ? failure.error_code : '',
    );
  }
  return data as T;
}
export async function api<T>(
  path: string,
  method = 'GET',
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const response = await fetch(path, {
    method,
    credentials: 'same-origin',
    signal,
    headers: { 'Content-Type': 'application/json', 'X-MagiSend': '1' },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  return responseData<T>(response);
}
export function formatBytes(n: number) {
  if (n < 1024) return `${formatNumber(n)} B`;
  for (const [divisor, unit] of [
    [1073741824, 'GiB'],
    [1048576, 'MiB'],
    [1024, 'KiB'],
  ] as const) {
    if (n >= divisor) return `${formatNumber(n / divisor, { maximumFractionDigits: 1 })} ${unit}`;
  }
  return `${formatNumber(n)} B`;
}
// A custom link name replaces the generated id in the visible link; the id link
// keeps working for anyone who already has it.
export function shareUrl(share: { id: string; slug?: string }) {
  return `${location.origin}/s/${share.slug || share.id}`;
}
