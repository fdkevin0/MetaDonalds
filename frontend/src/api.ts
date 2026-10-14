class RequestError extends Error {
  constructor(message: string, readonly status: number) { super(message); }
}

async function request(path: string, init?: RequestInit) {
  const response = await fetch(path, { ...init, redirect: 'error', signal: AbortSignal.timeout(60_000) });
  const body = await response.json().catch(cause => {
    if (response.ok) throw cause;
    return {};
  });
  if (!response.ok) {
    throw new RequestError(body?.detail || body?.title || `HTTP ${response.status}`, response.status);
  }
  return body;
}

export type Plugin = {
  id: string;
  type: string;
  kind: 'source' | 'downloader';
  capabilities: string[];
  resource_kinds: string[];
};
export type Item = { source_id: string; id: string; title: string; code?: string; summary?: string; score?: string; size?: string; seeders?: number; tags?: string[] };
export type Resource = { id: string; kind: 'magnet'; title: string; info_hash: string; size?: string; seeders?: number };
export type Submission = { status: 'accepted' | 'rejected' | 'unknown'; info_hash: string; message?: string };
export type ItemPage = { items: Item[]; next_cursor?: string };

export async function plugins(): Promise<Plugin[]> {
  const body = await request('/api/v1/plugins');
  if (!Array.isArray(body.plugins) || !body.plugins.every((p: Plugin) => p && typeof p.id === 'string' && typeof p.type === 'string' && ['source', 'downloader'].includes(p.kind) && Array.isArray(p.capabilities) && Array.isArray(p.resource_kinds))) throw new Error('插件响应格式错误。');
  return body.plugins;
}

export async function searchSource(source: string, query: string, cursor = '', filter = '', category = ''): Promise<ItemPage> {
  const q = query.trim();
  if (!q || new TextEncoder().encode(q).length > 200) throw new Error('关键词去除首尾空格后须为 1–200 字节。');
  const params = new URLSearchParams({ q });
  if (cursor) params.set('cursor', cursor);
  if (filter !== '') params.set('f', filter);
  if (category.trim()) params.set('c', category.trim());
  const body = await request(`/api/v1/sources/${encodeURIComponent(source)}/search?${params}`);
  if (!Array.isArray(body.items) || !body.items.every((item: Item) => item && item.source_id === source && typeof item.id === 'string' && typeof item.title === 'string') || (body.next_cursor !== undefined && typeof body.next_cursor !== 'string')) throw new Error('搜索响应格式错误。');
  return body;
}

function itemPath(item: Item) {
  return `/api/v1/sources/${encodeURIComponent(item.source_id)}/items/${encodeURIComponent(item.id)}`;
}

export async function detail(item: Item): Promise<{ item: Item; resources: Resource[] }> {
  const [value, body] = await Promise.all([request(itemPath(item)), request(`${itemPath(item)}/resources`)]);
  if (!value || value.id !== item.id || value.source_id !== item.source_id || !Array.isArray(body.resources) || !body.resources.every((resource: Resource) => resource && typeof resource.id === 'string' && typeof resource.title === 'string' && resource.kind === 'magnet' && /^[a-fA-F0-9]{40}$/.test(resource.info_hash))) throw new Error('详情响应格式错误。');
  return { item: value, resources: body.resources };
}

export function submissionKey(downloader: string, resource: Resource): string {
  return JSON.stringify([downloader, resource.info_hash.toLowerCase()]);
}

export async function submitResource(downloader: string, resource: Resource): Promise<Submission> {
  let body;
  try {
    body = await request(`/api/v1/downloaders/${encodeURIComponent(downloader)}/submissions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ resource }),
    });
  } catch (cause) {
    if (cause instanceof RequestError && [400, 403, 404, 405, 413, 415, 422].includes(cause.status)) {
      return { status: 'rejected', info_hash: resource.info_hash.toLowerCase(), message: cause.message };
    }
    throw cause;
  }
  if (!['accepted', 'rejected', 'unknown'].includes(body.status) || body.info_hash !== resource.info_hash.toLowerCase()) {
    throw new Error('提交响应格式错误。');
  }
  return body;
}
