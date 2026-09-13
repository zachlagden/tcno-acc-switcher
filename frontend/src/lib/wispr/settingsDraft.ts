export const STYLE_VALUES = ["", "formal", "casual", "veryCasual", "excited", "genz", "default"] as const;
export const CLEANUP_VALUES = ["none", "light", "medium", "high", "full"] as const;
export const STYLE_CATEGORIES = ["work", "email", "personal", "other"] as const;
export const TARGET_VALUES = ["all", "selected"] as const;

export type StyleCategory = (typeof STYLE_CATEGORIES)[number];

export type DraftEntry = {
  key: number;
  word: string;
  replacement: string;
  replacementHtml: string;
  isSnippet: boolean;
};

export type DraftVoice = {
  id: string;
  source: string;
  name: string;
  appNames: string[];
  autoCleanupLevel: string;
  stylePreference: string;
};

export type Draft = {
  parts: { styles: boolean; autoCleanup: boolean; voices: boolean; dictionary: boolean };
  styles: Record<StyleCategory, string>;
  autoCleanupLevel: string;
  userVoices: Record<string, DraftVoice>;
  dictionary: DraftEntry[];
  applyOnSwitch: boolean;
  target: string;
  selectedAccounts: string[];
  importedFrom: { displayName: string; at: string } | null;
};

type ProfileLike = {
  parts?: Partial<Draft["parts"]> | null;
  styles?: Partial<Record<StyleCategory, string>> | null;
  autoCleanupLevel?: string;
  userVoices?: Record<string, Partial<DraftVoice> | undefined> | null;
  dictionary?: ({ word?: string; replacement?: string; replacementHtml?: string; isSnippet?: boolean } | null)[] | null;
  applyOnSwitch?: boolean;
  target?: string;
  selectedAccounts?: string[] | null;
  importedFrom?: { displayName?: string; at?: string } | null;
};

let nextKey = 0;

export function newEntryKey(): number {
  nextKey += 1;
  return nextKey;
}

export function draftFromProfile(p: ProfileLike | null | undefined): Draft {
  const styles = p?.styles ?? {};
  const voices: Record<string, DraftVoice> = {};
  for (const [id, v] of Object.entries(p?.userVoices ?? {})) {
    if (!v) continue;
    voices[id] = {
      id: v.id || id,
      source: v.source || "builtIn",
      name: v.name || id,
      appNames: [...(v.appNames ?? [])],
      autoCleanupLevel: v.autoCleanupLevel ?? "",
      stylePreference: v.stylePreference ?? "",
    };
  }
  return {
    parts: {
      styles: !!p?.parts?.styles,
      autoCleanup: !!p?.parts?.autoCleanup,
      voices: !!p?.parts?.voices,
      dictionary: !!p?.parts?.dictionary,
    },
    styles: {
      work: styles.work ?? "",
      email: styles.email ?? "",
      personal: styles.personal ?? "",
      other: styles.other ?? "",
    },
    autoCleanupLevel: p?.autoCleanupLevel || "light",
    userVoices: voices,
    dictionary: (p?.dictionary ?? [])
      .filter((e): e is NonNullable<typeof e> => !!e)
      .map((e) => ({
        key: newEntryKey(),
        word: e.word ?? "",
        replacement: e.replacement ?? "",
        replacementHtml: e.replacementHtml ?? "",
        isSnippet: !!e.isSnippet,
      })),
    applyOnSwitch: !!p?.applyOnSwitch,
    target: p?.target === "selected" ? "selected" : "all",
    selectedAccounts: [...(p?.selectedAccounts ?? [])],
    importedFrom: p?.importedFrom?.displayName
      ? { displayName: p.importedFrom.displayName, at: p.importedFrom.at ?? "" }
      : null,
  };
}

export function profileFromDraft(d: Draft) {
  return {
    version: 1,
    parts: { ...d.parts },
    styles: { ...d.styles },
    autoCleanupLevel: d.autoCleanupLevel,
    userVoices: Object.fromEntries(Object.entries(d.userVoices).map(([id, v]) => [id, { ...v, appNames: [...v.appNames] }])),
    dictionary: d.dictionary
      .filter((e) => e.word.trim() !== "")
      .map((e) => ({
        word: e.word.trim(),
        replacement: e.replacement,
        replacementHtml: e.replacementHtml,
        isSnippet: e.isSnippet,
      })),
    applyOnSwitch: d.applyOnSwitch,
    target: d.target,
    selectedAccounts: [...d.selectedAccounts],
    importedFrom: d.importedFrom ? { ...d.importedFrom } : null,
  };
}

export function draftSignature(d: Draft): string {
  return JSON.stringify(profileFromDraft(d));
}

export function mergeImport(current: Draft, imported: ProfileLike): Draft {
  const incoming = draftFromProfile(imported);
  return {
    ...current,
    styles: incoming.styles,
    autoCleanupLevel: incoming.autoCleanupLevel,
    userVoices: Object.keys(incoming.userVoices).length > 0 ? incoming.userVoices : current.userVoices,
    dictionary: incoming.dictionary,
    importedFrom: incoming.importedFrom,
  };
}

export function entryProblem(d: Draft): "duplicate" | "snippet" | null {
  const seen = new Set<string>();
  for (const e of d.dictionary) {
    const word = e.word.trim();
    if (!word) continue;
    if (seen.has(word)) return "duplicate";
    seen.add(word);
    if (e.isSnippet && e.replacement.trim() === "") return "snippet";
  }
  return null;
}
