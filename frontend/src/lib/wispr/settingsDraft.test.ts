import { describe, expect, it } from "vitest";
import { draftFromProfile, draftSignature, entryProblem, mergeImport, profileFromDraft } from "./settingsDraft";

describe("settingsDraft", () => {
  it("round-trips a profile and drops blank dictionary rows", () => {
    const d = draftFromProfile({
      parts: { styles: true, dictionary: true },
      styles: { work: "casual" },
      autoCleanupLevel: "high",
      dictionary: [{ word: " brb ", replacement: "be right back", isSnippet: true }, { word: "  " }],
      target: "selected",
      selectedAccounts: ["a"],
    });
    const p = profileFromDraft(d);
    expect(p.parts).toEqual({ styles: true, autoCleanup: false, voices: false, dictionary: true });
    expect(p.styles).toEqual({ work: "casual", email: "", personal: "", other: "" });
    expect(p.dictionary).toEqual([{ word: "brb", replacement: "be right back", replacementHtml: "", isSnippet: true }]);
    expect(p.target).toBe("selected");
  });

  it("defaults unknown targets to all and cleanup to light", () => {
    const d = draftFromProfile({ target: "nope" });
    expect(d.target).toBe("all");
    expect(d.autoCleanupLevel).toBe("light");
  });

  it("imports data fields but keeps the user's apply settings", () => {
    const current = draftFromProfile({ applyOnSwitch: true, target: "selected", selectedAccounts: ["x"], parts: { voices: true } });
    const merged = mergeImport(current, {
      styles: { email: "formal" },
      autoCleanupLevel: "none",
      dictionary: [{ word: "Wispr" }],
      importedFrom: { displayName: "me", at: "2026-09-13T00:00:00Z" },
    });
    expect(merged.applyOnSwitch).toBe(true);
    expect(merged.selectedAccounts).toEqual(["x"]);
    expect(merged.parts.voices).toBe(true);
    expect(merged.styles.email).toBe("formal");
    expect(merged.dictionary.map((e) => e.word)).toEqual(["Wispr"]);
    expect(merged.importedFrom?.displayName).toBe("me");
    expect(draftSignature(merged)).not.toBe(draftSignature(current));
  });

  it("flags duplicate words and empty snippets", () => {
    expect(entryProblem(draftFromProfile({ dictionary: [{ word: "a" }, { word: "a" }] }))).toBe("duplicate");
    expect(entryProblem(draftFromProfile({ dictionary: [{ word: "s", isSnippet: true }] }))).toBe("snippet");
    expect(entryProblem(draftFromProfile({ dictionary: [{ word: "s", replacement: "x", isSnippet: true }] }))).toBeNull();
  });
});
