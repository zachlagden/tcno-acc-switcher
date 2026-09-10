<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { get } from "svelte/store";
  import { previousPage, appBarTitle, navigateBackLikeButton } from "../stores/nav";
  import { t, locale } from "../stores/i18n";
  import { offlineMode } from "../stores/offlineMode";
  import { activeModal, openConfirm } from "../stores/modal";
  import { collapse, DUR } from "../lib/animation";
  import { tooltip } from "../lib/actions/tooltip";
  import { formatLastLoginForLocale } from "../lib/formatLastLogin";
  import { formatCount, formatSpeakingTime, formatWpm, shareOf } from "../lib/wispr/statsFormat";
  import { GetWisprFlowStats } from "../../bindings/TcNo-Acc-Switcher/internal/basic/basicservice.js";
  import type {
    AccountReport,
    Breakdown,
    Report,
    Stats,
  } from "../../bindings/TcNo-Acc-Switcher/internal/wisprstats/models";
  import "../styles/Settings.scss";
  import "../styles/wisprStats.scss";

  const EMPTY = "–";

  let report: Report | null = null;
  let loading = true;
  let refreshing = false;
  let loadError = "";
  let expanded = new Set<string>();
  let requestSeq = 0;
  let destroyed = false;

  $: appBarTitle.set($t("WisprStats_Title"));
  $: rows = (report?.accounts ?? []) as AccountReport[];
  $: totals = report?.totals ?? null;
  $: busy = loading || refreshing;
  $: showShare = (totals?.accounts ?? 0) > 1;

  type Split = { key: string; label: string; data: Breakdown };

  function splitsOf(s: Stats | null | undefined): Split[] {
    const out: Split[] = [];
    if (s?.desktop) out.push({ key: "desktop", label: $t("WisprStats_Desktop"), data: s.desktop });
    if (s?.mobile) out.push({ key: "mobile", label: $t("WisprStats_Mobile"), data: s.mobile });
    return out;
  }

  function when(raw: string | undefined): string {
    if (!raw) return $t("WisprStats_Never");
    return formatLastLoginForLocale(raw, $locale) || $t("WisprStats_Never");
  }

  function errorText(e: unknown): string {
    if (e instanceof Error) return e.message;
    return String(e);
  }

  async function load(live: boolean): Promise<void> {
    const seq = ++requestSeq;
    if (live) {
      refreshing = true;
    } else {
      loading = true;
    }
    loadError = "";
    try {
      const next = await GetWisprFlowStats(live);
      if (destroyed || seq !== requestSeq) return;
      report = next;
    } catch (e) {
      if (destroyed || seq !== requestSeq) return;
      loadError = `${$t("WisprStats_LoadFailed")}: ${errorText(e)}`;
    } finally {
      if (!destroyed && seq === requestSeq) {
        loading = false;
        refreshing = false;
      }
    }
  }

  async function refreshLive(): Promise<void> {
    if (busy) return;
    if (get(offlineMode)) {
      const ok = await openConfirm({
        title: $t("WisprStats_Offline_Title"),
        body: $t("WisprStats_Offline_Body"),
        style: "yesno",
        positiveLabel: $t("WisprStats_RefreshLive"),
        negativeLabel: $t("No"),
      });
      if (!ok) return;
    }
    await load(true);
  }

  function toggleExpanded(id: string): void {
    const next = new Set(expanded);
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    expanded = next;
  }

  function onClose(): void {
    navigateBackLikeButton();
  }

  function onWindowKeyDown(e: KeyboardEvent): void {
    if (get(activeModal)) return;
    if (e.key === "Escape") {
      e.preventDefault();
      onClose();
    }
  }

  onMount(() => {
    previousPage.set({ page: "platform", platformName: "Wispr Flow" });
    void load(false);
  });

  onDestroy(() => {
    destroyed = true;
  });
</script>

<svelte:window on:keydown={onWindowKeyDown} />

<div class="main-content main-spacing wisprstats-root">
  <h1 class="SettingsHeader">{$t("WisprStats_Title")}</h1>
  <p class="wisprstats-note">{$t("WisprStats_Note")}</p>

  <div class="wisprstats-toolbar">
    <span class="wisprstats-mode" aria-live="polite">
      <span class="wisprstats-mode__dot" class:is-live={!refreshing && report?.live} aria-hidden="true"></span>
      {#if refreshing}
        {$t("WisprStats_Refreshing")}
      {:else if report?.live}
        {$t("WisprStats_Mode_Live", { time: when(report.generatedAt) })}
      {:else}
        {$t("WisprStats_Mode_Snapshot")}
      {/if}
    </span>
    <button
      type="button"
      class="btnicontext wisprstats-refresh"
      class:is-busy={refreshing}
      disabled={busy || rows.length === 0}
      on:click={() => void refreshLive()}
    >
      <svg viewBox="0 0 512 512" aria-hidden="true">
        <path
          d="M463.5 224H472c13.3 0 24-10.7 24-24V72c0-9.7-5.8-18.5-14.8-22.2s-19.3-1.7-26.2 5.2L413.4 96.6c-87.6-86.5-228.7-86.2-315.8 1c-87.5 87.5-87.5 229.3 0 316.8s229.3 87.5 316.8 0c12.5-12.5 12.5-32.8 0-45.3s-32.8-12.5-45.3 0c-62.5 62.5-163.8 62.5-226.3 0s-62.5-163.8 0-226.3c62.2-62.2 162.7-62.5 225.3-1l-30.4 30.4c-6.9 6.9-8.9 17.2-5.2 26.2s12.5 14.8 22.2 14.8H463.5z"
        />
      </svg>{$t("WisprStats_RefreshLive")}
    </button>
  </div>

  {#if loadError}
    <div class="wisprstats-error" role="alert">{loadError}</div>
  {/if}

  <div class="wisprstats-table" role="table" aria-label={$t("WisprStats_Title")} aria-busy={busy}>
    <div class="wisprstats-row wisprstats-row--head" role="row">
      <span role="columnheader">{$t("WisprStats_Col_Account")}</span>
      <span role="columnheader" class="is-num">{$t("WisprStats_Col_Words")}</span>
      <span role="columnheader" class="is-num">{$t("WisprStats_Col_ThisWeek")}</span>
      <span role="columnheader" class="is-num">{$t("WisprStats_Col_Wpm")}</span>
      <span role="columnheader" class="is-num">{$t("WisprStats_Col_Speaking")}</span>
      <span role="columnheader" class="is-num">{$t("WisprStats_Col_Streak")}</span>
      <span role="columnheader" class="is-num">{$t("WisprStats_Col_Last")}</span>
    </div>

    {#if loading}
      <div class="wisprstats-empty" aria-live="polite">{$t("WisprStats_Loading")}</div>
    {:else if rows.length === 0}
      <div class="wisprstats-empty">{$t("WisprStats_Empty")}</div>
    {:else}
      {#each rows as row (row.uniqueId)}
        {@const s = row.stats}
        {@const splits = splitsOf(s)}
        {@const isOpen = expanded.has(row.uniqueId)}
        <div class="wisprstats-row" role="row">
          <span role="cell" class="wisprstats-account">
            <span class="wisprstats-account__line">
              {#if splits.length > 0}
                <button
                  type="button"
                  class="wisprstats-expander"
                  aria-expanded={isOpen}
                  aria-label={$t("WisprStats_ShowBreakdown")}
                  use:tooltip={$t("WisprStats_ShowBreakdown")}
                  on:click={() => toggleExpanded(row.uniqueId)}
                >
                  <span class="wisprstats-chevron" class:is-open={isOpen} aria-hidden="true">▸</span>
                </button>
              {/if}
              <span class="wisprstats-account__name" title={row.displayName}>{row.displayName}</span>
              {#if row.current}
                <span class="wisprstats-badge is-current">{$t("WisprStats_Current")}</span>
              {/if}
              {#if s}
                <span class="wisprstats-badge">{$t(`WisprStats_Source_${row.source}`)}</span>
              {/if}
            </span>
            {#if row.problem}
              <span class="wisprstats-problem" title={$t(`WisprStats_Problem_${row.problem}`)}>
                {$t(`WisprStats_Problem_${row.problem}`)}
              </span>
            {/if}
          </span>
          {#if s}
            <span role="cell" class="is-num">
              {formatCount(s.totalWords, $locale)}
              {#if showShare && totals}
                <span class="wisprstats-share" aria-hidden="true">
                  <span style="transform: scaleX({shareOf(s.totalWords, totals.totalWords)})"></span>
                </span>
              {/if}
            </span>
            <span role="cell" class="is-num">{formatCount(s.wordsThisWeek, $locale)}</span>
            <span role="cell" class="is-num">{formatWpm(s.wordsPerMinute)}</span>
            <span role="cell" class="is-num">{formatSpeakingTime(s.speakingSeconds)}</span>
            <span role="cell" class="is-num">{$t("WisprStats_Streak", { days: s.dayStreak, weeks: s.weekStreak })}</span>
            <span role="cell" class="is-num">{when(s.lastDictationAt)}</span>
          {:else}
            {#each Array(6) as _}
              <span role="cell" class="is-num wisprstats-subtle">{EMPTY}</span>
            {/each}
          {/if}
        </div>

        {#if isOpen}
          {#each splits as split (split.key)}
            <div
              class="wisprstats-row wisprstats-row--split"
              role="row"
              transition:collapse={{ duration: DUR.fast }}
            >
              <span role="cell" class="wisprstats-account">{split.label}</span>
              <span role="cell" class="is-num">{formatCount(split.data.totalWords, $locale)}</span>
              <span role="cell" class="is-num">{formatCount(split.data.wordsThisWeek, $locale)}</span>
              <span role="cell" class="is-num">{formatWpm(split.data.wordsPerMinute)}</span>
              <span role="cell" class="is-num">{formatSpeakingTime(split.data.speakingSeconds)}</span>
              <span role="cell" class="is-num"></span>
              <span role="cell" class="is-num">{when(split.data.lastDictationAt)}</span>
            </div>
          {/each}
        {/if}
      {/each}

      {#if totals && totals.accounts > 0}
        <div class="wisprstats-row wisprstats-row--total" role="row">
          <span role="cell">{$t("WisprStats_Total", { count: totals.accounts })}</span>
          <span role="cell" class="is-num">{formatCount(totals.totalWords, $locale)}</span>
          <span role="cell" class="is-num">{formatCount(totals.wordsThisWeek, $locale)}</span>
          <span role="cell" class="is-num">{formatWpm(totals.wordsPerMinute)}</span>
          <span role="cell" class="is-num">{formatSpeakingTime(totals.speakingSeconds)}</span>
          <span role="cell" class="is-num wisprstats-subtle">
            {$t("WisprStats_BestStreak", { days: totals.bestDayStreak, weeks: totals.bestWeekStreak })}
          </span>
          <span role="cell" class="is-num">{when(totals.lastDictationAt)}</span>
        </div>
      {/if}
    {/if}
  </div>

  <div class="buttoncol col_close wisprstats-footer">
    <button type="button" class="btn_close" on:click={onClose}><span>{$t("Button_Close")}</span></button>
  </div>
</div>
