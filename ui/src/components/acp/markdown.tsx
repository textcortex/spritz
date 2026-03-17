import { useMemo } from 'react';

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function renderInline(text: string): string {
  let result = escapeHtml(text);
  // Bold
  result = result.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
  // Italic
  result = result.replace(/(?<!\*)\*(?!\*)(.+?)(?<!\*)\*(?!\*)/g, '<em>$1</em>');
  // Strikethrough
  result = result.replace(/~~(.+?)~~/g, '<del>$1</del>');
  // Inline code
  result = result.replace(/`([^`]+)`/g, '<code class="rounded bg-muted px-1 py-0.5 text-sm">$1</code>');
  // Links
  result = result.replace(
    /\[([^\]]+)\]\(([^)]+)\)/g,
    '<a href="$2" target="_blank" rel="noopener noreferrer" class="text-primary underline">$1</a>',
  );
  return result;
}

function renderMarkdownToHtml(text: string): string {
  const lines = text.split('\n');
  const output: string[] = [];
  let inCodeBlock = false;
  let codeContent: string[] = [];
  let codeLang = '';
  let inList = false;
  let listType: 'ul' | 'ol' = 'ul';

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];

    // Code blocks
    if (line.startsWith('```')) {
      if (!inCodeBlock) {
        if (inList) {
          output.push(listType === 'ul' ? '</ul>' : '</ol>');
          inList = false;
        }
        inCodeBlock = true;
        codeLang = line.slice(3).trim();
        codeContent = [];
        continue;
      } else {
        inCodeBlock = false;
        const langAttr = codeLang ? ` data-lang="${escapeHtml(codeLang)}"` : '';
        output.push(
          `<div class="my-2 overflow-x-auto rounded-md border bg-muted/50">` +
            (codeLang ? `<div class="border-b px-3 py-1 text-[10px] text-muted-foreground">${escapeHtml(codeLang)}</div>` : '') +
            `<pre class="p-3 text-sm"><code${langAttr}>${escapeHtml(codeContent.join('\n'))}</code></pre></div>`,
        );
        continue;
      }
    }
    if (inCodeBlock) {
      codeContent.push(line);
      continue;
    }

    // Empty line
    if (!line.trim()) {
      if (inList) {
        output.push(listType === 'ul' ? '</ul>' : '</ol>');
        inList = false;
      }
      continue;
    }

    // Headings
    const headingMatch = line.match(/^(#{1,6})\s+(.+)/);
    if (headingMatch) {
      if (inList) {
        output.push(listType === 'ul' ? '</ul>' : '</ol>');
        inList = false;
      }
      const level = headingMatch[1].length;
      const sizes = ['text-2xl', 'text-xl', 'text-lg', 'text-base', 'text-sm', 'text-xs'];
      output.push(`<h${level} class="font-semibold ${sizes[level - 1]} mt-3 mb-1">${renderInline(headingMatch[2])}</h${level}>`);
      continue;
    }

    // Blockquote
    if (line.startsWith('> ')) {
      if (inList) {
        output.push(listType === 'ul' ? '</ul>' : '</ol>');
        inList = false;
      }
      output.push(`<blockquote class="border-l-2 border-muted-foreground/30 pl-3 text-muted-foreground italic">${renderInline(line.slice(2))}</blockquote>`);
      continue;
    }

    // Unordered list
    const ulMatch = line.match(/^(\s*)[-*+]\s+(.+)/);
    if (ulMatch) {
      if (!inList || listType !== 'ul') {
        if (inList) output.push(listType === 'ul' ? '</ul>' : '</ol>');
        output.push('<ul class="list-disc pl-5 space-y-0.5">');
        inList = true;
        listType = 'ul';
      }
      output.push(`<li>${renderInline(ulMatch[2])}</li>`);
      continue;
    }

    // Ordered list
    const olMatch = line.match(/^(\s*)\d+\.\s+(.+)/);
    if (olMatch) {
      if (!inList || listType !== 'ol') {
        if (inList) output.push(listType === 'ul' ? '</ul>' : '</ol>');
        output.push('<ol class="list-decimal pl-5 space-y-0.5">');
        inList = true;
        listType = 'ol';
      }
      output.push(`<li>${renderInline(olMatch[2])}</li>`);
      continue;
    }

    // Horizontal rule
    if (/^---+$/.test(line.trim())) {
      if (inList) {
        output.push(listType === 'ul' ? '</ul>' : '</ol>');
        inList = false;
      }
      output.push('<hr class="my-2 border-border" />');
      continue;
    }

    // Paragraph
    if (inList) {
      output.push(listType === 'ul' ? '</ul>' : '</ol>');
      inList = false;
    }
    output.push(`<p class="leading-relaxed">${renderInline(line)}</p>`);
  }

  if (inList) output.push(listType === 'ul' ? '</ul>' : '</ol>');
  if (inCodeBlock && codeContent.length) {
    output.push(
      `<div class="my-2 overflow-x-auto rounded-md border bg-muted/50"><pre class="p-3 text-sm"><code>${escapeHtml(codeContent.join('\n'))}</code></pre></div>`,
    );
  }

  return output.join('\n');
}

interface MarkdownProps {
  content: string;
  className?: string;
}

export function Markdown({ content, className = '' }: MarkdownProps) {
  const html = useMemo(() => renderMarkdownToHtml(content), [content]);
  return (
    <div
      className={`prose prose-sm max-w-none dark:prose-invert ${className}`}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}
