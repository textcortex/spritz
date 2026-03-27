function decodeHtmlEntities(text: string): string {
  return String(text || '')
    .replace(/&nbsp;/gi, ' ')
    .replace(/&amp;/gi, '&')
    .replace(/&lt;/gi, '<')
    .replace(/&gt;/gi, '>')
    .replace(/&quot;/gi, '"')
    .replace(/&#39;/gi, "'");
}

function stripHtmlTags(text: string): string {
  return decodeHtmlEntities(
    String(text || '')
      .replace(/<script[\s\S]*?<\/script>/gi, ' ')
      .replace(/<style[\s\S]*?<\/style>/gi, ' ')
      .replace(/<[^>]+>/g, ' '),
  )
    .replace(/\s+/g, ' ')
    .trim();
}

function extractHtmlTagText(html: string, tagName: string): string {
  const match = String(html || '').match(new RegExp(`<${tagName}[^>]*>([\\s\\S]*?)<\\/${tagName}>`, 'i'));
  return match ? stripHtmlTags(match[1]) : '';
}

export function summarizeHtmlErrorDocument(text: string): string | null {
  const raw = String(text || '').trim();
  if (!raw) return null;
  if (!/^\s*<(?:!doctype\s+html|html\b)/i.test(raw)) return null;

  const title = extractHtmlTagText(raw, 'title');
  const flattened = stripHtmlTags(raw);
  const codeMatch = flattened.match(/\berror code\s+(\d{3})\b/i) || title.match(/\b(\d{3})\b/);
  const hostMatches = [...flattened.matchAll(/\b([a-z0-9.-]+\.[a-z]{2,})\b/gi)].map((m) => m[1]);
  const host =
    hostMatches.sort((a, b) => b.length - a.length).find((value) => !/^cloudflare\.com$/i.test(value)) || '';
  const providerMatch = flattened.match(/\b(Cloudflare|Vercel|Netlify|nginx|Apache)\b/i);
  const summaryMatch =
    flattened.match(/\bThe web server reported [^.]+\./i) ||
    flattened.match(/\bThis page isn['']t working[^.]*\./i) ||
    flattened.match(/\bBad gateway\b/i);

  const parts: string[] = [];
  if (codeMatch?.[1]) parts.push(`HTTP ${codeMatch[1]}`);
  if (title) parts.push(title);
  else if (summaryMatch?.[0]) parts.push(summaryMatch[0]);
  else parts.push('HTML error response');
  if (host) parts.push(host);
  if (providerMatch?.[1]) parts.push(providerMatch[1]);

  return parts.join(' · ');
}

export function normalizeHtmlErrorText(text: string): string {
  return summarizeHtmlErrorDocument(text) || text;
}
