<script lang="ts">
  import { onMount } from 'svelte';
  import { plugins, searchSource, detail, submitResource, submissionKey, type Plugin, type Item, type Resource, type Submission } from './api';

  let available = $state<Plugin[]>([]);
  let loadingPlugins = $state(false);
  let source = $state('');
  let downloader = $state('');
  let query = $state('');
  let filter = $state('');
  let category = $state('');
  let results = $state<Item[]>([]);
  let searched = $state(false);
  let searching = $state(false);
  let loadingDetail = $state(false);
  let submitting = $state(false);
  let error = $state('');
  let notice = $state('');
  let lastQuery = $state('');
  let searchFilter = '';
  let searchCategory = '';
  let cursors = $state(['']);
  let page = $state(0);
  let nextCursor = $state('');
  let current = $state<Item | null>(null);
  let resources = $state<Resource[]>([]);
  let selected = $state<Resource | null>(null);
  let selectedDownloader = $state('');
  let submissions = $state<Record<string, Submission['status']>>({});
  let dialog: HTMLDialogElement;
  let searchVersion = 0;
  let detailVersion = 0;
  const sources = $derived(available.filter(p => p.kind === 'source' && p.capabilities.includes('search')));
  const downloaders = $derived(available.filter(p => p.kind === 'downloader' && p.capabilities.includes('submit') && p.resource_kinds.includes('magnet')));
  const isNyaa = $derived(sources.find(p => p.id === source)?.type === 'nyaa');
  const message = (cause: unknown) => cause instanceof Error ? cause.message : '请求失败，请检查服务连接。';

  let mounted = true;
  async function loadPlugins() {
    loadingPlugins = true;
    error = '';
    try {
      const value = await plugins();
      if (!mounted) return;
      available = value;
      source = sources[0]?.id ?? '';
      downloader = downloaders[0]?.id ?? '';
    } catch (cause) {
      if (mounted) error = message(cause);
    } finally {
      if (mounted) loadingPlugins = false;
    }
  }

  onMount(() => {
    void loadPlugins();
    return () => { mounted = false; searchVersion++; detailVersion++; };
  });

  function resetSource() {
    searchVersion++;
    detailVersion++;
    results = [];
    current = null;
    resources = [];
    searched = searching = loadingDetail = false;
    cursors = [''];
    page = 0;
    nextCursor = '';
    error = notice = '';
  }

  async function loadPage(cursor: string, index: number) {
    const version = ++searchVersion;
    detailVersion++;
    current = null;
    resources = [];
    loadingDetail = false;
    searching = true;
    error = notice = '';
    try {
      const result = await searchSource(source, lastQuery, cursor, searchFilter, searchCategory);
      if (version !== searchVersion) return;
      results = result.items;
      nextCursor = result.next_cursor ?? '';
      page = index;
      cursors = [...cursors.slice(0, index), cursor];
      searched = true;
    } catch (cause) {
      if (version === searchVersion) error = message(cause);
    } finally {
      if (version === searchVersion) searching = false;
    }
  }

  function runSearch(event: SubmitEvent) {
    event.preventDefault();
    if (searching || submitting || !source) return;
    lastQuery = query.trim();
    searchFilter = isNyaa ? filter : '';
    searchCategory = isNyaa ? category : '';
    results = [];
    searched = false;
    nextCursor = '';
    cursors = [''];
    page = 0;
    void loadPage('', 0);
  }

  async function openItem(item: Item) {
    const version = ++detailVersion;
    current = item;
    resources = [];
    loadingDetail = true;
    error = '';
    try {
      const value = await detail(item);
      if (version !== detailVersion) return;
      const nyaa = sources.find(p => p.id === item.source_id)?.type === 'nyaa';
      current = nyaa ? { ...value.item, ...item } : value.item;
      resources = value.resources.map(resource => nyaa ? { ...resource, title: item.title, size: item.size, seeders: item.seeders } : resource);
    } catch (cause) {
      if (version === detailVersion) error = message(cause);
    } finally {
      if (version === detailVersion) loadingDetail = false;
    }
  }

  function confirm(resource: Resource) {
    if (submitting || !downloader || submissions[submissionKey(downloader, resource)] === 'accepted') return;
    selected = resource;
    selectedDownloader = downloader;
    dialog.showModal();
  }

  async function runSubmit() {
    if (!selected || submitting) return;
    const resource = selected;
    const target = selectedDownloader;
    const key = submissionKey(target, resource);
    submitting = true;
    error = notice = '';
    try {
      const result = await submitResource(target, resource);
      submissions[key] = result.status;
      if (result.status === 'accepted') notice = `${target} 已接受：${resource.title}。这不表示下载已完成。`;
      else if (result.status === 'rejected') error = `提交被拒绝：${result.message || '下载器未接受命令。'}`;
      else error = `无法确认 ${target} 的提交结果。请先检查下载器；再次提交可能产生重复任务。`;
    } catch {
      submissions[key] = 'unknown';
      error = `无法确认 ${target} 的提交结果。请先检查下载器；没有自动重试，再次提交可能产生重复任务。`;
    } finally {
      submitting = false;
      dialog.close();
      selected = null;
    }
  }
</script>

<svelte:head><meta name="description" content="搜索数据源，查看资源并提交至下载器。" /></svelte:head>

<div class="mx-auto max-w-5xl px-4 py-6 sm:px-8 sm:py-8">
  <header class="mb-6 border-b border-neutral-200 pb-4"><h1 class="text-xl font-semibold">MetaDonalds</h1></header>
  <main>
    {#if !sources.length}
      <p class="mb-3 text-sm text-neutral-500">{loadingPlugins ? '正在加载插件…' : '没有可用数据源，请检查服务配置。'}</p>
      <button class="secondary mb-4" disabled={loadingPlugins} onclick={loadPlugins}>重新加载插件</button>
    {/if}
    <form onsubmit={runSearch}>
      <div class="grid items-end gap-3 sm:grid-cols-[auto_1fr_auto]">
        <label class="space-y-2 text-sm"><span class="block">数据源</span><select class="field" bind:value={source} onchange={resetSource} disabled={submitting || !sources.length}>{#each sources as plugin}<option value={plugin.id}>{plugin.id}</option>{/each}</select></label>
        <label class="space-y-2 text-sm"><span class="block">搜索关键词</span><input class="field" bind:value={query} placeholder="输入关键词" required disabled={submitting} /></label>
        <button class="primary min-w-24" disabled={searching || submitting || !source} type="submit">{searching ? '搜索中…' : '搜索'}</button>
      </div>
      {#if isNyaa}
        <div class="mt-4 grid gap-3 sm:grid-cols-2">
          <label class="space-y-2 text-sm"><span class="block">资源筛选</span><select class="field" bind:value={filter} disabled={submitting}><option value="">使用服务配置</option><option value="0">全部资源</option><option value="1">排除重制</option><option value="2">仅可信资源</option></select></label>
          <label class="space-y-2 text-sm"><span class="block">分类代码（可选）</span><input class="field" bind:value={category} pattern="[0-9]+_[0-9]+" placeholder="留空使用服务配置，如 0_0" disabled={submitting} /></label>
        </div>
      {/if}
    </form>
    {#if error}<p role="alert" class="mt-5 border-l-2 border-black pl-3 text-sm leading-6"><strong>错误：</strong>{error}</p>{/if}
    <div role="status" aria-live="polite">{#if notice}<p class="mt-5 border-l-2 border-black pl-3 text-sm leading-6">{notice}</p>{/if}{#if searching}<p class="mt-6 text-sm text-neutral-600">正在搜索…</p>{/if}</div>
    <section class="mt-8" aria-label="搜索结果" aria-busy={searching}>
      {#if searched}<h2 class="mb-3 break-words text-sm font-medium">“{lastQuery}” · 第 {page + 1} 页 · {results.length} 条结果</h2>{/if}
      <ul class="divide-y divide-neutral-200 border-t border-neutral-200">
        {#each results as item}
          <li class="flex items-center justify-between gap-4 py-4"><div><p class="break-words text-sm leading-6">{item.title}</p>{#if item.tags?.length}<p class="mt-1 text-xs text-neutral-600">{item.tags.join(' · ')}</p>{/if}{#if item.code || item.score}<p class="text-xs text-neutral-500">{item.code ?? ''} {item.score ? `评分 ${item.score}` : ''}</p>{/if}{#if item.size || item.seeders !== undefined}<p class="text-xs text-neutral-500">{item.size ?? ''} {item.seeders !== undefined ? `做种 ${item.seeders}` : ''}</p>{/if}</div><button class="secondary shrink-0" disabled={submitting || searching} onclick={() => openItem(item)} aria-label={`查看 ${item.title} 详情`}>详情 / 资源</button></li>
        {:else}
          {#if !searching}<li class="py-6 text-sm text-neutral-500">{searched ? '没有找到资源，请尝试其他关键词或筛选条件。' : '选择数据源并输入关键词开始搜索。'}</li>{/if}
        {/each}
      </ul>
      {#if searched && (page > 0 || nextCursor)}<nav aria-label="结果分页" class="mt-4 flex gap-3"><button class="secondary" disabled={page === 0 || searching || submitting} onclick={() => loadPage(cursors[page - 1], page - 1)}>上一页</button><button class="secondary" disabled={!nextCursor || searching || submitting} onclick={() => loadPage(nextCursor, page + 1)}>下一页</button></nav>{/if}
    </section>
    {#if current}
      <section class="mt-8 border-t border-neutral-200 pt-5" aria-label="条目详情与资源" aria-busy={loadingDetail}>
        <h2 class="break-words font-semibold">{current.title}</h2>
        {#if current.tags?.length}<p class="mt-2 text-xs text-neutral-600">{current.tags.join(' · ')}</p>{/if}
        {#if current.summary}<p class="mt-3 whitespace-pre-wrap text-sm leading-6">{current.summary}</p>{/if}
        <label class="mt-4 block max-w-sm space-y-2 text-sm"><span class="block">下载器</span><select class="field" bind:value={downloader} disabled={submitting || !downloaders.length}>{#each downloaders as plugin}<option value={plugin.id}>{plugin.id}</option>{/each}</select></label>
        {#if !downloaders.length}<p class="mt-2 text-sm text-neutral-500">没有启用支持磁链的下载器。</p>{/if}
        {#if loadingDetail}<p role="status" class="mt-4 text-sm">正在加载详情与资源…</p>{:else}
          <ul class="mt-4 divide-y divide-neutral-200">
            {#each resources as resource}
              {@const status = submissions[submissionKey(downloader, resource)]}
              <li class="flex items-center justify-between gap-4 py-4"><div class="min-w-0"><p class="break-words text-sm">{resource.title}</p><p class="mt-1 break-all font-mono text-xs text-neutral-500">{resource.info_hash}</p><p class="mt-1 text-xs text-neutral-500">{resource.size ?? ''} {resource.seeders !== undefined ? `做种 ${resource.seeders}` : ''}</p>{#if status === 'unknown'}<p class="mt-1 text-xs font-semibold">提交结果不确定，请先检查下载器。</p>{:else if status === 'rejected'}<p class="mt-1 text-xs">上次提交被拒绝。</p>{/if}</div><button class="secondary shrink-0" disabled={submitting || !downloader || resource.kind !== 'magnet' || status === 'accepted'} onclick={() => confirm(resource)} aria-label={`提交 ${resource.title} 到 ${downloader}`}>{status === 'accepted' ? '已接受' : '提交'}</button></li>
            {:else}<li class="py-4 text-sm text-neutral-500">没有可用资源。</li>{/each}
          </ul>
        {/if}
      </section>
    {/if}
  </main>
</div>
<dialog bind:this={dialog} onclose={() => { selected = null; }} aria-labelledby="confirm-title" oncancel={(event) => { if (submitting) event.preventDefault(); }} class="fixed inset-0 m-auto w-[min(92vw,480px)] rounded border border-black bg-white p-6 text-black backdrop:bg-black/40">
  <h2 id="confirm-title" class="text-lg font-semibold">提交到 {selectedDownloader}？</h2>
  <p class="mt-4 break-words text-sm leading-6">{selected?.title}</p><p class="mt-2 break-all font-mono text-xs text-neutral-500">{selected?.info_hash}</p>
  <p class="mt-4 text-sm leading-6">{selected && submissions[submissionKey(selectedDownloader, selected)] === 'unknown' ? '上次提交结果不确定。请先检查下载器；再次提交可能创建重复任务。' : '确认后将提交命令。命令被接受不代表下载完成，请在下载器查看进度。'}</p>
  <div class="mt-6 flex justify-end gap-3"><button class="secondary" disabled={submitting} onclick={() => dialog.close()}>取消</button><button class="primary" disabled={submitting} onclick={runSubmit}>{submitting ? '提交中…' : '确认提交'}</button></div>
</dialog>
