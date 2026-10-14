import assert from 'node:assert/strict';
import { afterEach, mock, spyOn, test } from 'bun:test';

afterEach(() => mock.restore());

test('plugin search encodes identities and opaque cursors while preserving Nyaa overrides', async () => {
  const { searchSource } = await import('../src/api.ts');
  spyOn(globalThis, 'fetch').mockImplementation(async (path: string) => {
    const url = new URL(path, 'http://localhost');
    assert.equal(url.pathname, '/api/v1/sources/nyaa%2Fcustom/search');
    assert.equal(url.searchParams.get('q'), '猫');
    assert.equal(url.searchParams.get('cursor'), 'a+b=/');
    assert.equal(url.searchParams.get('f'), '0');
    assert.equal(url.searchParams.get('c'), '0_0');
    return Response.json({ items: [], next_cursor: 'next' });
  });
  assert.deepEqual(await searchSource('nyaa/custom', ' 猫 ', 'a+b=/', '0', ' 0_0 '), { items: [], next_cursor: 'next' });
});

test('generic submission separates rejection and uncertainty without retry and validates accepted identity', async () => {
  const { submitResource, submissionKey } = await import('../src/api.ts');
  const resource = { id: 'r', kind: 'magnet' as const, title: 'test', info_hash: 'A'.repeat(40) };
  let response = Response.json({ detail: 'unsupported resource' }, { status: 422 });
  const fetch = spyOn(globalThis, 'fetch').mockImplementation(async (path: string, init: RequestInit) => {
    assert.equal(path, '/api/v1/downloaders/pikpak-main/submissions');
    assert.equal(init.redirect, 'error');
    assert.deepEqual(JSON.parse(String(init.body)), { resource });
    return response;
  });
  assert.equal((await submitResource('pikpak-main', resource)).status, 'rejected');
  response = Response.json({ status: 'unknown', info_hash: 'a'.repeat(40) });
  assert.equal((await submitResource('pikpak-main', resource)).status, 'unknown');
  response = Response.json({ status: 'accepted', info_hash: 'b'.repeat(40) });
  await assert.rejects(submitResource('pikpak-main', resource), /格式错误/);
  response = Response.json({ title: 'bad gateway' }, { status: 502 });
  await assert.rejects(submitResource('pikpak-main', resource));
  assert.equal(fetch.mock.calls.length, 4);
  fetch.mockImplementation(async () => { throw new TypeError('network failure'); });
  await assert.rejects(submitResource('pikpak-main', resource), /network failure/);
  assert.equal(fetch.mock.calls.length, 5);
  assert.equal(submissionKey('one', resource), submissionKey('one', { ...resource, info_hash: resource.info_hash.toLowerCase() }));
  assert.notEqual(submissionKey('one', resource), submissionKey('two', resource));
});

test('detail requests both data and resources and rejects cross-item responses', async () => {
  const { detail } = await import('../src/api.ts');
  const item = { source_id: 'javdb', id: 'abc', title: 'test' };
  const paths: string[] = [];
  spyOn(globalThis, 'fetch').mockImplementation(async (path: string) => {
    paths.push(path);
    return Response.json(path.endsWith('/resources') ? { resources: [] } : { ...item, id: 'wrong' });
  });
  await assert.rejects(detail(item), /格式错误/);
  assert.deepEqual(paths.sort(), ['/api/v1/sources/javdb/items/abc', '/api/v1/sources/javdb/items/abc/resources']);
});

test('source results preserve generic tags and resource metadata for detail display', async () => {
  const { searchSource } = await import('../src/api.ts');
  const item = { source_id: 'nyaa', id: 'a'.repeat(40), title: 'release', tags: ['可信', '重制'], size: '2 GiB', seeders: 0 };
  spyOn(globalThis, 'fetch').mockResolvedValue(Response.json({ items: [item] }));
  const page = await searchSource('nyaa', 'release');
  assert.deepEqual(page.items[0], item);
});

test('search trims the query and validates UTF-8 bytes before calling the source', async () => {
  const { searchSource } = await import('../src/api.ts');
  const fetch = spyOn(globalThis, 'fetch').mockImplementation(async (path: string) => {
    assert.equal(new URL(path, 'http://localhost').searchParams.get('q'), '猫 & dogs');
    return Response.json({ items: [] });
  });
  await searchSource('nyaa', ' 猫 & dogs ');
  await assert.rejects(searchSource('nyaa', '猫'.repeat(67)));
  await assert.rejects(searchSource('nyaa', '  '));
  assert.equal(fetch.mock.calls.length, 1);
});

test('submission classifies boundary refusals even when their bodies are not JSON', async () => {
  const { submitResource } = await import('../src/api.ts');
  const resource = { id: 'r', kind: 'magnet' as const, title: 'test', info_hash: 'A'.repeat(40) };
  let response = new Response('forbidden', { status: 403 });
  const fetch = spyOn(globalThis, 'fetch').mockImplementation(async () => response);
  for (const status of [403, 405, 413, 415]) {
    for (response of [Response.json({ detail: 'refused' }, { status }), new Response('refused', { status }), Response.json(null, { status })]) {
      const result = await submitResource('test', resource);
      assert.equal(result.status, 'rejected');
      assert.equal(result.info_hash, 'a'.repeat(40));
    }
  }
  assert.equal(fetch.mock.calls.length, 12);
  response = new Response('bad gateway', { status: 502 });
  await assert.rejects(submitResource('test', resource));
});
