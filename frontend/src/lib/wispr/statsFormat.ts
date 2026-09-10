export function formatCount(value: number, locale?: string): string {
  if (!Number.isFinite(value)) return "0";
  return new Intl.NumberFormat(locale).format(Math.round(value));
}

export function formatWpm(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0";
  return String(Math.round(value));
}

export function formatSpeakingTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "0m";
  const totalMinutes = Math.floor(seconds / 60);
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (hours === 0) return `${totalMinutes}m`;
  return `${hours}h ${String(minutes).padStart(2, "0")}m`;
}

export function shareOf(part: number, whole: number): number {
  if (!Number.isFinite(part) || !Number.isFinite(whole) || whole <= 0 || part <= 0) return 0;
  return Math.min(1, part / whole);
}
