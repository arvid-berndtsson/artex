export const mentionKinds = [
  { kind: "finding", label: "Finding", alias: "finding", aliases: ["漏洞"] },
  { kind: "asset", label: "Asset", alias: "asset", aliases: ["资产"] },
  { kind: "company", label: "Company", alias: "company", aliases: ["企业"] },
  { kind: "endpoint", label: "Endpoint", alias: "api", aliases: ["接口"] },
  { kind: "ip", label: "IP", alias: "ip", aliases: [] },
  { kind: "app", label: "Application", alias: "app", aliases: ["应用"] },
  { kind: "root_domain", label: "Domain", alias: "domain", aliases: ["域名"] },
  { kind: "subdomain", label: "Subdomain", alias: "subdomain", aliases: ["子域名"] },
  { kind: "service", label: "Service", alias: "service", aliases: ["服务"] },
] as const;

export type MentionKind = (typeof mentionKinds)[number]["kind"];
export interface ChatMention {
  kind: MentionKind;
  id: number;
  label: string;
  description: string;
}

export function activeMention(value: string, caret: number) {
  const before = value.slice(0, caret);
  const start = before.lastIndexOf("@");
  if (start < 0 || (start > 0 && /[\w.+/-]/.test(before[start - 1]))) return null;
  const query = before.slice(start + 1);
  if (/[[\]\r\n@]/.test(query) || query.length > 220) return null;
  return { start, end: caret, query };
}

export function mentionSearch(query: string) {
  const text = query.trimStart().toLowerCase();
  for (const item of mentionKinds) {
    for (const alias of [item.label.toLowerCase(), item.alias, ...item.aliases.map((value) => value.toLowerCase())]) {
      if (text === alias || text.startsWith(`${alias} `) || (/[^a-z]/.test(alias) && text.startsWith(alias))) {
        return { kind: item.kind, query: query.trimStart().slice(alias.length).trim(), categories: [] };
      }
    }
  }
  const categories = mentionKinds.filter(
    (item) => [item.label.toLowerCase(), item.alias, ...item.aliases.map((value) => value.toLowerCase())].some((term) => term.startsWith(text)),
  );
  return { kind: "" as const, query: query.trim(), categories };
}

export function mentionToken(item: ChatMention) {
  const kind = mentionKinds.find((entry) => entry.kind === item.kind)?.label ?? "Asset";
  const label = item.label
    .replace(/[[\]]/g, (char) => (char === "[" ? "(" : ")"))
    .replace(/\s+/g, " ")
    .slice(0, 100);
  return `@[${kind}#${item.id} ${label}]`;
}

export function selectedMentions(value: string) {
  return [...value.matchAll(/@\[([^#\]]+)#([0-9]+)(?: ([^\]\r\n]*))?\]/g)].map((match) => {
    const kind = mentionKinds.find(
      (entry) => entry.label.toLowerCase() === match[1].toLowerCase() || entry.alias === match[1].toLowerCase() || (entry.aliases as readonly string[]).includes(match[1]),
    )?.label ?? match[1];
    return {
      token: match[0],
      label: `${kind} #${match[2]}${match[3] ? ` · ${match[3]}` : ""}`,
      start: match.index,
    };
  });
}
