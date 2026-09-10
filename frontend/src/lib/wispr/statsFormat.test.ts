import { describe, expect, it } from "vitest";
import { formatCount, formatSpeakingTime, formatWpm, shareOf } from "./statsFormat";

describe("formatCount", () => {
  it("groups thousands for the locale", () => {
    expect(formatCount(218125, "en-US")).toBe("218,125");
  });
  it("rounds and survives bad input", () => {
    expect(formatCount(12.6, "en-US")).toBe("13");
    expect(formatCount(Number.NaN, "en-US")).toBe("0");
  });
});

describe("formatWpm", () => {
  it("rounds to a whole number", () => {
    expect(formatWpm(139.84)).toBe("140");
  });
  it("shows zero for no data", () => {
    expect(formatWpm(0)).toBe("0");
    expect(formatWpm(Number.POSITIVE_INFINITY)).toBe("0");
  });
});

describe("formatSpeakingTime", () => {
  it("uses hours and padded minutes past an hour", () => {
    expect(formatSpeakingTime(96333)).toBe("26h 45m");
    expect(formatSpeakingTime(3660)).toBe("1h 01m");
  });
  it("uses minutes under an hour", () => {
    expect(formatSpeakingTime(2700)).toBe("45m");
    expect(formatSpeakingTime(59)).toBe("0m");
  });
  it("treats missing time as zero", () => {
    expect(formatSpeakingTime(-5)).toBe("0m");
  });
});

describe("shareOf", () => {
  it("returns a clamped fraction", () => {
    expect(shareOf(25, 100)).toBe(0.25);
    expect(shareOf(150, 100)).toBe(1);
  });
  it("is zero when there is nothing to share", () => {
    expect(shareOf(5, 0)).toBe(0);
    expect(shareOf(-1, 10)).toBe(0);
  });
});
