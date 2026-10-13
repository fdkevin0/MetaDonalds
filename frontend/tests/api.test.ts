import assert from 'node:assert/strict';
import { afterEach, mock, spyOn, test } from 'bun:test';
import { search, submit } from '../src/api.ts';

afterEach(() => mock.restore());

test('search keeps explicit zero filters, omits defaults, and validates UTF-8 bytes', async () => {
  const fetch = spyOn(globalThis, 'fetch').mockImplementation(async (path: string) => {
    const url = new URL(path, 'http://localhost');
    assert.equal(url.searchParams.get('q'), '猫 & dogs');
    assert.equal(url.searchParams.get('f'), '0');
    assert.equal(url.searchParams.has('c'), false);
    return Response.json({ results: [] });
  });
  assert.deepEqual(await search(' 猫 & dogs ', '0', ''), []);
  await assert.rejects(search('猫'.repeat(67), '', ''));
  assert.equal(fetch.mock.calls.length, 1);
});

test('submission never retries, rejects redirects, and requires an echoed accepted hash', async () => {
  const hash = '0123456789abcdef0123456789abcdef01234567';
  let response = Response.json({ detail: 'timeout' }, { status: 502 });
  const fetch = spyOn(globalThis, 'fetch').mockImplementation(async (path: string, init: RequestInit) => {
    assert.equal(path, '/api/v1/submit');
    assert.equal(init.method, 'POST');
    assert.equal(init.redirect, 'error');
    assert.deepEqual(JSON.parse(String(init.body)), { info_hash: hash });
    return response;
  });
  await assert.rejects(submit(hash), /timeout/);
  assert.equal(fetch.mock.calls.length, 1);
  response = Response.json({ status: 'submitted' });
  await assert.rejects(submit(hash), /格式错误/);
  response = Response.json({ status: 'submitted', info_hash: hash });
  await submit(hash);
  assert.equal(fetch.mock.calls.length, 3);
});

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
