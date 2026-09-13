<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { get } from "svelte/store";
  import { previousPage, appBarTitle, navigateBackLikeButton } from "../stores/nav";
  import { t, locale } from "../stores/i18n";
  import { offlineMode } from "../stores/offlineMode";
  import { activeModal, openConfirm } from "../stores/modal";
  import { formatLastLoginForLocale } from "../lib/formatLastLogin";
  import SettingsGroup from "../components/settings/SettingsGroup.svelte";
  import SettingsToggle from "../components/settings/SettingsToggle.svelte";
  import ProcessMethodDropdown from "../components/settings/ProcessMethodDropdown.svelte";
  import {
    CLEANUP_VALUES,
    STYLE_CATEGORIES,
    STYLE_VALUES,
    TARGET_VALUES,
    draftFromProfile,
    draftSignature,
    entryProblem,
    mergeImport,
    newEntryKey,
    profileFromDraft,
    type Draft,
    type StyleCategory,
  } from "../lib/wispr/settingsDraft";
  import {
    ApplyWisprSettings,
    GetWisprSettings,
    ImportWisprSettings,
    SaveWisprSettings,
  } from "../../bindings/TcNo-Acc-Switcher/internal/basic/basicservice.js";
  import type {
    WisprApplyReport,
    WisprSettingsAccount,
  } from "../../bindings/TcNo-Acc-Switcher/internal/basic/models";
  import {
    Profile,
    type AccountResult,
    type ImportResult,
    type PartResult,
  } from "../../bindings/TcNo-Acc-Switcher/internal/wisprsettings/models";
  import "../styles/Settings.scss";
  import "../styles/wisprSettings.scss";

  let draft: Draft = draftFromProfile(null);
  let savedSig = draftSignature(draft);
  let accounts: WisprSettingsAccount[] = [];
  let importChoice = "";
  let importInfo: ImportResult | null = null;
  let report: WisprApplyReport | null = null;
  let loading = true;
  let saving = false;
  let importing = false;
  let applying = false;
  let errorText = "";
  let destroyed = false;

  $: appBarTitle.set($t("WisprSettings_Title"));
  $: dirty = draftSignature(draft) !== savedSig;
  $: busy = loading || saving || importing || applying;
  $: dictProblem = entryProblem(draft);
  $: anyPart = draft.parts.styles || draft.parts.autoCleanup || draft.parts.voices || draft.parts.dictionary;
  $: voiceList = Object.values(draft.userVoices);
  $: importOptions = accounts.map((a) => a.uniqueId);
  $: if (!importChoice && accounts.length > 0) importChoice = (accounts.find((a) => a.current) ?? accounts[0]).uniqueId;

  function message(e: unknown): string {
    return e instanceof Error ? e.message : String(e);
  }

  function accountName(uid: string): string {
    const a = accounts.find((x) => x.uniqueId === uid);
    if (!a) return uid;
    return a.current ? $t("WisprSettings_AccountCurrent", { name: a.displayName }) : a.displayName;
  }

  function styleLabel(v: string): string {
    return v ? $t(`WisprSettings_Style_${v}`) : $t("WisprSettings_Style_keep");
  }

  function cleanupLabel(v: string): string {
    return $t(`WisprSettings_Cleanup_${v}`);
  }

  function when(raw: string | undefined): string {
    if (!raw) return "";
    return formatLastLoginForLocale(raw, $locale) || raw;
  }

  function setStyle(category: StyleCategory, value: string): void {
    draft.styles = { ...draft.styles, [category]: value };
  }

  function toggleSelected(uid: string, on: boolean): void {
    const rest = draft.selectedAccounts.filter((x) => x !== uid);
    draft.selectedAccounts = on ? [...rest, uid] : rest;
  }

  function addEntry(): void {
    draft.dictionary = [...draft.dictionary, { key: newEntryKey(), word: "", replacement: "", replacementHtml: "", isSnippet: false }];
  }

  function removeEntry(key: number): void {
    draft.dictionary = draft.dictionary.filter((e) => e.key !== key);
  }

  async function load(): Promise<void> {
    loading = true;
    errorText = "";
    try {
      const state = await GetWisprSettings();
      if (destroyed) return;
      accounts = (state.accounts ?? []) as WisprSettingsAccount[];
      draft = draftFromProfile(state.profile);
      savedSig = draftSignature(draft);
    } catch (e) {
      if (!destroyed) errorText = `${$t("WisprSettings_LoadFailed")}: ${message(e)}`;
    } finally {
      if (!destroyed) loading = false;
    }
  }

  async function save(): Promise<boolean> {
    if (dictProblem) {
      errorText = $t(`WisprSettings_Dict_Problem_${dictProblem}`);
      return false;
    }
    saving = true;
    errorText = "";
    try {
      const saved = await SaveWisprSettings(Profile.createFrom(profileFromDraft(draft)));
      if (destroyed) return false;
      draft = draftFromProfile(saved);
      savedSig = draftSignature(draft);
      return true;
    } catch (e) {
      if (!destroyed) errorText = `${$t("WisprSettings_SaveFailed")}: ${message(e)}`;
      return false;
    } finally {
      if (!destroyed) saving = false;
    }
  }

  async function runImport(): Promise<void> {
    if (busy || !importChoice) return;
    importing = true;
    errorText = "";
    try {
      const result = await ImportWisprSettings(importChoice);
      if (destroyed) return;
      importInfo = result;
      draft = mergeImport(draft, result.profile ?? {});
    } catch (e) {
      if (!destroyed) errorText = `${$t("WisprSettings_ImportFailed")}: ${message(e)}`;
    } finally {
      if (!destroyed) importing = false;
    }
  }

  async function apply(): Promise<void> {
    if (busy || !anyPart) return;
    if (get(offlineMode)) {
      const ok = await openConfirm({
        title: $t("WisprSettings_Offline_Title"),
        body: $t("WisprSettings_Offline_Body"),
        style: "yesno",
        positiveLabel: $t("WisprSettings_Apply"),
        negativeLabel: $t("No"),
      });
      if (!ok) return;
    }
    if (dirty && !(await save())) return;
    applying = true;
    errorText = "";
    report = null;
    try {
      const next = await ApplyWisprSettings();
      if (!destroyed) report = next;
    } catch (e) {
      if (!destroyed) errorText = `${$t("WisprSettings_ApplyFailed")}: ${message(e)}`;
    } finally {
      if (!destroyed) applying = false;
    }
  }

  function partText(p: PartResult | undefined, kind: PartKind): string {
    if (!p) return "";
    if (kind === "dictionary" && p.status === "applied") {
      return $t("WisprSettings_Result_DictApplied", { added: p.added, updated: p.updated });
    }
    return $t(`WisprSettings_Result_${p.status}`);
  }

  type PartKind = "prefs" | "dictionary" | "voices";

  function partsOf(row: AccountResult): [PartKind, PartResult | undefined][] {
    return [
      ["prefs", row.prefs],
      ["dictionary", row.dictionary],
      ["voices", row.voices],
    ];
  }

  function onClose(): void {
    navigateBackLikeButton();
  }

  function onWindowKeyDown(e: KeyboardEvent): void {
    if (get(activeModal)) return;
    const target = e.target as HTMLElement | null;
    if (target?.closest("input, textarea, .dropdown.show")) return;
    if (e.key === "Escape") {
      e.preventDefault();
      onClose();
    }
  }

  onMount(() => {
    previousPage.set({ page: "platform", platformName: "Wispr Flow" });
    void load();
  });

  onDestroy(() => {
    destroyed = true;
  });
</script>

<svelte:window on:keydown={onWindowKeyDown} />

<div class="main-content main-spacing wisprsettings-root">
  <h1 class="SettingsHeader">{$t("WisprSettings_Title")}</h1>
  <p class="wisprsettings-note">{$t("WisprSettings_Note")}</p>

  {#if errorText}
    <div class="wisprsettings-error" role="alert">{errorText}</div>
  {/if}

  {#if loading}
    <p class="wisprsettings-note" aria-live="polite">{$t("WisprSettings_Loading")}</p>
  {:else if accounts.length === 0}
    <p class="wisprsettings-note">{$t("WisprSettings_Empty")}</p>
  {:else}
    <div class="wisprsettings-body">
      <SettingsGroup title={$t("WisprSettings_Header_Import")}>
        <div class="wisprsettings-import">
          <ProcessMethodDropdown
            label={$t("WisprSettings_ImportFrom")}
            values={importOptions}
            current={importChoice}
            labelFn={accountName}
            disabled={busy}
            on:select={(e) => (importChoice = e.detail.value)}
          />
          <button type="button" class="btnicontext" disabled={busy || !importChoice} on:click={() => void runImport()}>
            {importing ? $t("WisprSettings_Importing") : $t("WisprSettings_Import")}
          </button>
        </div>
        {#if importInfo}
          <p class="wisprsettings-note" aria-live="polite">
            {$t("WisprSettings_ImportDone", { name: importInfo.profile?.importedFrom?.displayName ?? "" })}
            {#if importInfo.prefsSource === "local"}
              {$t("WisprSettings_ImportLocal")}
            {/if}
            {#if importInfo.excluded > 0}
              {$t("WisprSettings_ImportExcluded", { count: importInfo.excluded })}
            {/if}
          </p>
        {:else if draft.importedFrom}
          <p class="wisprsettings-note">
            {$t("WisprSettings_ImportedFrom", { name: draft.importedFrom.displayName, time: when(draft.importedFrom.at) })}
          </p>
        {/if}
      </SettingsGroup>

      <SettingsGroup title={$t("WisprSettings_Header_Parts")}>
        <SettingsToggle
          id="ws-part-styles"
          label={$t("WisprSettings_Part_Styles")}
          checked={draft.parts.styles}
          on:change={(e) => (draft.parts.styles = e.detail)}
        />
        <SettingsToggle
          id="ws-part-cleanup"
          label={$t("WisprSettings_Part_Cleanup")}
          checked={draft.parts.autoCleanup}
          on:change={(e) => (draft.parts.autoCleanup = e.detail)}
        />
        <SettingsToggle
          id="ws-part-voices"
          label={$t("WisprSettings_Part_Voices")}
          tooltip={$t("WisprSettings_Part_Voices_Tip")}
          checked={draft.parts.voices}
          disabled={voiceList.length === 0}
          on:change={(e) => (draft.parts.voices = e.detail)}
        />
        <SettingsToggle
          id="ws-part-dictionary"
          label={$t("WisprSettings_Part_Dictionary")}
          tooltip={$t("WisprSettings_Part_Dictionary_Tip")}
          checked={draft.parts.dictionary}
          on:change={(e) => (draft.parts.dictionary = e.detail)}
        />
      </SettingsGroup>

      <SettingsGroup title={$t("WisprSettings_Header_Styles")}>
        {#each STYLE_CATEGORIES as category (category)}
          <ProcessMethodDropdown
            label={$t(`WisprSettings_Category_${category}`)}
            values={STYLE_VALUES}
            current={draft.styles[category]}
            labelFn={styleLabel}
            disabled={!draft.parts.styles}
            on:select={(e) => setStyle(category, e.detail.value)}
          />
        {/each}
        <ProcessMethodDropdown
          label={$t("WisprSettings_Cleanup")}
          values={CLEANUP_VALUES}
          current={draft.autoCleanupLevel}
          labelFn={cleanupLabel}
          disabled={!draft.parts.autoCleanup}
          on:select={(e) => (draft.autoCleanupLevel = e.detail.value)}
        />
      </SettingsGroup>

      <SettingsGroup title={$t("WisprSettings_Header_Voices")}>
        {#if voiceList.length === 0}
          <p class="wisprsettings-note">{$t("WisprSettings_Voices_Empty")}</p>
        {:else}
          <ul class="wisprsettings-voices" class:is-off={!draft.parts.voices}>
            {#each voiceList as voice (voice.id)}
              <li>
                <span class="wisprsettings-voices__name">{voice.name}</span>
                <span>{styleLabel(voice.stylePreference)}, {cleanupLabel(voice.autoCleanupLevel || "light")}</span>
                <span class="wisprsettings-subtle" title={voice.appNames.join(", ")}>
                  {voice.appNames.length > 0 ? voice.appNames.join(", ") : $t("WisprSettings_Voices_NoApps")}
                </span>
              </li>
            {/each}
          </ul>
        {/if}
      </SettingsGroup>

      <SettingsGroup title={$t("WisprSettings_Header_Dictionary")}>
        <div class="wisprsettings-dict" class:is-off={!draft.parts.dictionary} role="table" aria-label={$t("WisprSettings_Header_Dictionary")}>
          <div class="wisprsettings-dict__row wisprsettings-dict__row--head" role="row">
            <span role="columnheader">{$t("WisprSettings_Dict_Word")}</span>
            <span role="columnheader">{$t("WisprSettings_Dict_Replacement")}</span>
            <span role="columnheader">{$t("WisprSettings_Dict_Snippet")}</span>
            <span role="columnheader" aria-label={$t("WisprSettings_Dict_Remove")}></span>
          </div>
          {#each draft.dictionary as entry, i (entry.key)}
            <div class="wisprsettings-dict__row" role="row">
              <span role="cell">
                <input
                  type="text"
                  spellcheck="false"
                  autocomplete="off"
                  maxlength="255"
                  aria-label={$t("WisprSettings_Dict_Word")}
                  bind:value={draft.dictionary[i].word}
                />
              </span>
              <span role="cell">
                <input
                  type="text"
                  spellcheck="false"
                  autocomplete="off"
                  maxlength="10000"
                  aria-label={$t("WisprSettings_Dict_Replacement")}
                  placeholder={entry.isSnippet ? "" : $t("WisprSettings_Dict_ReplacementNone")}
                  bind:value={draft.dictionary[i].replacement}
                  on:input={() => (draft.dictionary[i].replacementHtml = "")}
                />
              </span>
              <span role="cell" class="wisprsettings-dict__check">
                <input
                  type="checkbox"
                  aria-label={$t("WisprSettings_Dict_Snippet")}
                  bind:checked={draft.dictionary[i].isSnippet}
                />
              </span>
              <span role="cell">
                <button
                  type="button"
                  class="wisprsettings-dict__remove"
                  aria-label={$t("WisprSettings_Dict_Remove")}
                  title={$t("WisprSettings_Dict_Remove")}
                  on:click={() => removeEntry(entry.key)}>×</button
                >
              </span>
            </div>
          {:else}
            <div class="wisprsettings-dict__empty">{$t("WisprSettings_Dict_Empty")}</div>
          {/each}
        </div>
        <div class="wisprsettings-dict__actions">
          <button type="button" class="btnicontext" on:click={addEntry}>{$t("WisprSettings_Dict_Add")}</button>
          {#if dictProblem}
            <span class="wisprsettings-problem">{$t(`WisprSettings_Dict_Problem_${dictProblem}`)}</span>
          {/if}
        </div>
      </SettingsGroup>

      <SettingsGroup title={$t("WisprSettings_Header_When")}>
        <SettingsToggle
          id="ws-apply-on-switch"
          label={$t("WisprSettings_ApplyOnSwitch")}
          tooltip={$t("WisprSettings_ApplyOnSwitch_Tip")}
          span
          checked={draft.applyOnSwitch}
          on:change={(e) => (draft.applyOnSwitch = e.detail)}
        />
        <ProcessMethodDropdown
          label={$t("WisprSettings_Target")}
          values={TARGET_VALUES}
          current={draft.target}
          labelFn={(v) => $t(`WisprSettings_Target_${v}`)}
          on:select={(e) => (draft.target = e.detail.value)}
        />
        {#if draft.target === "selected"}
          {#each accounts as account (account.uniqueId)}
            <SettingsToggle
              id={`ws-acc-${account.uniqueId}`}
              label={accountName(account.uniqueId)}
              checked={draft.selectedAccounts.includes(account.uniqueId)}
              on:change={(e) => toggleSelected(account.uniqueId, e.detail)}
            />
          {/each}
        {/if}
      </SettingsGroup>

      {#if report}
        <SettingsGroup title={$t("WisprSettings_Header_Results")}>
          {#if report.closeFailed}
            <p class="wisprsettings-problem">{$t("WisprSettings_CloseFailed")}</p>
          {:else if report.relaunched}
            <p class="wisprsettings-note">{$t("WisprSettings_Relaunched")}</p>
          {/if}
          {#if (report.results ?? []).length === 0}
            <p class="wisprsettings-note">{$t("WisprSettings_Result_None")}</p>
          {:else}
            <div class="wisprsettings-results" role="table" aria-label={$t("WisprSettings_Header_Results")}>
              <div class="wisprsettings-results__row wisprsettings-results__row--head" role="row">
                <span role="columnheader">{$t("WisprSettings_Col_Account")}</span>
                <span role="columnheader">{$t("WisprSettings_Col_Prefs")}</span>
                <span role="columnheader">{$t("WisprSettings_Col_Dictionary")}</span>
                <span role="columnheader">{$t("WisprSettings_Col_Voices")}</span>
              </div>
              {#each report.results ?? [] as row (row.uniqueId)}
                <div class="wisprsettings-results__row" role="row">
                  <span role="cell" class="wisprsettings-results__name" title={row.displayName}>{accountName(row.uniqueId)}</span>
                  {#each partsOf(row) as [kind, part] (kind)}
                    <span role="cell" class={`wisprsettings-status is-${part?.status ?? "off"}`}>
                      {partText(part, kind)}
                      {#if part?.problem}
                        <span class="wisprsettings-problem">{$t(`WisprSettings_Problem_${part.problem}`)}</span>
                      {/if}
                    </span>
                  {/each}
                </div>
              {/each}
            </div>
          {/if}
        </SettingsGroup>
      {/if}
    </div>
  {/if}

  <div class="buttoncol col_close wisprsettings-footer">
    {#if !loading && accounts.length > 0}
      <span class="wisprsettings-dirty" aria-live="polite">
        {#if applying}
          {$t("WisprSettings_Applying")}
        {:else if dirty}
          {$t("WisprSettings_Unsaved")}
        {/if}
      </span>
      <button
        type="button"
        class="btnicontext"
        disabled={busy || !anyPart}
        on:click={() => void apply()}
      >
        {dirty ? $t("WisprSettings_SaveAndApply") : $t("WisprSettings_Apply")}
      </button>
      <button type="button" class="btnicontext" disabled={busy || !dirty} on:click={() => void save()}>
        {saving ? $t("WisprSettings_Saving") : $t("WisprSettings_Save")}
      </button>
    {/if}
    <button type="button" class="btn_close" on:click={onClose}><span>{$t("Button_Close")}</span></button>
  </div>
</div>
