export type Torrent = {
  title: string;
  info_hash: string;
  seeders: number;
  size: string;
  trusted: boolean;
  remake: boolean;
  score: number;
};

async function request(path: string, init?: RequestInit) {
  const response = await fetch(path, { ...init, redirect: 'error', signal: AbortSignal.timeout(60_000) });
  const body = await response.json();
  if (!response.ok) {
    throw new Error(body.detail || body.title || `HTTP ${response.status}`);
  }
  return body;
}

export async function search(query: string, filter: string, category: string): Promise<Torrent[]> {
  const q = query.trim();
  if (!q || new TextEncoder().encode(q).length > 200) {
    throw new Error('关键词去除首尾空格后须为 1–200 字节。');
  }
  const params = new URLSearchParams({ q });
  if (filter !== '') params.set('f', filter);
  if (category.trim() !== '') params.set('c', category.trim());
  const body = await request(`/api/v1/search?${params}`);
  if (!Array.isArray(body.results)) throw new Error('搜索响应格式错误。');
  return body.results;
}

export async function submit(hash: string): Promise<void> {
  const body = await request('/api/v1/submit', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ info_hash: hash }),
  });
  if (body.status !== 'submitted' || body.info_hash !== hash.toLowerCase()) {
    throw new Error('提交响应格式错误。');
  }
}
