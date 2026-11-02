import type { AskResponse, RequestBody, SearchResponse } from './types';

/** Settings persisted in localStorage: tenant id + API key the Go service enforces. */
export interface Settings {
  tenant: string;
  apiKey: string;
}

const STORAGE_KEY = 'prismsearch.settings';

export function loadSettings(): Settings {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) return { tenant: '', apiKey: '', ...(JSON.parse(raw) as Partial<Settings>) };
  } catch {
    // ignore malformed storage
  }
  return { tenant: '', apiKey: '' };
}

export function saveSettings(s: Settings) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(s));
}

/** In dev, Vite proxies /api → :8080; in the container, nginx does the same. */
const BASE = '/api';

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
    this.name = 'ApiError';
  }
}

function headers(s: Settings): HeadersInit {
  const h: Record<string, string> = { 'Content-Type': 'application/json' };
  if (s.apiKey) h['X-API-Key'] = s.apiKey;
  if (s.tenant) h['X-Tenant-ID'] = s.tenant;
  return h;
}

async function post<T>(path: string, body: RequestBody, s: Settings): Promise<T> {
  let resp: Response;
  try {
    resp = await fetch(`${BASE}${path}`, {
      method: 'POST',
      headers: headers(s),
      body: JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, 'Cannot reach the query service. Is the stack running?');
  }
  const data = (await resp.json().catch(() => ({}))) as Record<string, unknown>;
  if (!resp.ok) {
    const msg = typeof data.error === 'string' ? data.error : `request failed (${resp.status})`;
    throw new ApiError(resp.status, msg);
  }
  return data as T;
}

export function search(body: RequestBody, s: Settings) {
  return post<SearchResponse>('/search', body, s);
}

export function ask(body: RequestBody, s: Settings) {
  return post<AskResponse>('/ask', body, s);
}

export async function health(): Promise<boolean> {
  try {
    const r = await fetch(`${BASE}/health`);
    return r.ok;
  } catch {
    return false;
  }
}
