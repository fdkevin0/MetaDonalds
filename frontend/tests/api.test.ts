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
