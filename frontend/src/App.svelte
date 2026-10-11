<script lang="ts">
  import { search, submit, type Torrent } from './api';

  let query = $state('');
  let filter = $state('');
  let category = $state('');
  let results = $state<Torrent[]>([]);
  let searched = $state(false);
  let searching = $state(false);
  let submitting = $state(false);
  let error = $state('');
  let notice = $state('');
  let lastQuery = $state('');
  let selected = $state<Torrent | null>(null);
  let submissions = $state<Record<string, 'accepted' | 'uncertain'>>({});
  let dialog: HTMLDialogElement;

  async function runSearch(event: SubmitEvent) {
    event.preventDefault();
    if (searching || submitting) return;
    searching = true;
    error = '';
    notice = '';
    results = [];
    searched = false;
    try {
      results = await search(query, filter, category);
      lastQuery = query.trim();
      searched = true;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : '搜索失败，请检查服务连接。';
    } finally {
      searching = false;
    }
  }

  function confirm(item: Torrent) {
    if (submitting || submissions[item.info_hash] === 'accepted') return;
    selected = item;
    dialog.showModal();
  }

  async function runSubmit() {
    if (!selected || submitting) return;
    const item = selected;
    submitting = true;
    error = '';
    notice = '';
    try {
      await submit(item.info_hash);
      submissions[item.info_hash] = 'accepted';
      notice = `命令已接受：${item.title}。这不表示下载已完成。`;
    } catch {
      submissions[item.info_hash] = 'uncertain';
      error = '未能确认提交结果，请先在 PikPak 检查是否已创建任务。没有自动重试；再次提交可能产生重复任务。';
    } finally {
      submitting = false;
      dialog.close();
      selected = null;
    }
  }
</script>

<svelte:head><meta name="description" content="搜索 Nyaa，选择资源并提交至 PikPak。" /></svelte:head>

<div class="mx-auto max-w-5xl px-4 py-6 sm:px-8 sm:py-8">
  <header class="mb-6 border-b border-neutral-200 pb-4">
    <h1 class="text-xl font-semibold">MetaDonalds</h1>
  </header>

  <main>
    <form onsubmit={runSearch}>
      <div class="grid items-end gap-3 sm:grid-cols-[1fr_auto]">
        <label class="space-y-2 text-sm">
          <span class="block">搜索关键词</span>
          <input class="field" bind:value={query} placeholder="输入关键词" required disabled={submitting} />
        </label>
        <button class="primary min-w-24" disabled={searching || submitting} type="submit">{searching ? '搜索中…' : '搜索'}</button>
      </div>
      <div class="mt-4 grid gap-3 sm:grid-cols-2">
        <label class="space-y-2 text-sm">
          <span class="block">资源筛选</span>
          <select class="field" bind:value={filter} disabled={submitting}>
            <option value="">使用服务配置</option>
            <option value="0">全部资源</option>
            <option value="1">排除重制</option>
            <option value="2">仅可信资源</option>
          </select>
        </label>
        <label class="space-y-2 text-sm">
          <span class="block">分类代码 <span class="text-neutral-500">（可选）</span></span>
          <input class="field" bind:value={category} pattern="[0-9]+_[0-9]+" placeholder="留空使用服务配置，如 0_0" disabled={submitting} />
        </label>
      </div>
    </form>

    {#if error}<p role="alert" class="mt-5 border-l-2 border-black pl-3 text-sm leading-6"><strong>错误：</strong>{error}</p>{/if}
    <div role="status" aria-live="polite">
      {#if notice}<p class="mt-5 border-l-2 border-black pl-3 text-sm leading-6">{notice}</p>{/if}
      {#if searching}<p class="mt-6 text-sm text-neutral-600">正在搜索…</p>{/if}
    </div>

    <section class="mt-8" aria-label="搜索结果" aria-busy={searching}>
      {#if searched}
        <h2 class="mb-3 break-words text-sm font-medium">“{lastQuery}” · {results.length} 条结果</h2>
      {/if}
      {#if results.length}
        <div class="overflow-x-auto">
          <table class="w-full text-left text-sm">
            <thead class="border-y border-neutral-200 text-neutral-600"><tr><th class="py-3 pr-4 font-normal">资源名称</th><th class="px-3 py-3 font-normal">大小</th><th class="px-3 py-3 font-normal whitespace-nowrap">做种 / 评分</th><th class="py-3 pl-3"><span class="sr-only">操作</span></th></tr></thead>
            <tbody class="divide-y divide-neutral-200">
              {#each results as item}
                <tr>
                  <td class="min-w-56 max-w-xl py-4 pr-4">
                    <p class="break-words leading-6">{item.title}</p>
                    {#if item.trusted || item.remake || submissions[item.info_hash] === 'uncertain'}
                      <div class="mt-1 flex flex-wrap gap-3 text-xs text-neutral-600">
                        {#if item.trusted}<span>可信</span>{/if}
                        {#if item.remake}<span>重制</span>{/if}
                        {#if submissions[item.info_hash] === 'uncertain'}<span class="font-semibold text-black">提交结果不确定</span>{/if}
                      </div>
                    {/if}
                  </td>
                  <td class="whitespace-nowrap px-3 py-4 text-neutral-600">{item.size}</td>
                  <td class="whitespace-nowrap px-3 py-4 tabular-nums">{item.seeders} / {item.score}</td>
                  <td class="py-4 pl-3 text-right"><button type="button" class="secondary whitespace-nowrap" onclick={() => confirm(item)} disabled={submitting || submissions[item.info_hash] === 'accepted'} aria-label={`提交 ${item.title} 到 PikPak`}>{submissions[item.info_hash] === 'accepted' ? '已接受' : '提交'}</button></td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else if !searching}
        <p class="border-t border-neutral-200 py-6 text-sm text-neutral-500">{searched ? '没有找到资源，请尝试其他关键词或筛选条件。' : '输入关键词开始搜索。'}</p>
      {/if}
    </section>
  </main>
</div>

<dialog bind:this={dialog} aria-labelledby="confirm-title" oncancel={(event) => { if (submitting) event.preventDefault(); }} class="fixed inset-0 m-auto w-[min(92vw,480px)] rounded border border-black bg-white p-6 text-black backdrop:bg-black/40">
  <h2 id="confirm-title" class="text-lg font-semibold">提交到 PikPak？</h2>
  <p class="mt-4 break-words text-sm leading-6">{selected?.title}</p>
  <p class="mt-2 break-all font-mono text-xs text-neutral-500">{selected?.info_hash}</p>
  <p class="mt-4 text-sm leading-6">{selected && submissions[selected.info_hash] === 'uncertain' ? '上次提交结果不确定。请先检查 PikPak；再次提交可能创建重复任务。' : '确认后将创建任务。命令被接受不代表下载完成，请在 PikPak 查看进度。'}</p>
  <div class="mt-6 flex justify-end gap-3"><button class="secondary" disabled={submitting} onclick={() => dialog.close()}>取消</button><button class="primary" disabled={submitting} onclick={runSubmit}>{submitting ? '提交中…' : '确认提交'}</button></div>
</dialog>
