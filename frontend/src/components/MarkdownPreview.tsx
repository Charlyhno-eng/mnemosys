import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

type HastNode = {
  type: string;
  value?: string;
  tagName?: string;
  properties?: Record<string, unknown>;
  children?: HastNode[];
};

// Only color spans are accepted as raw HTML. All other toolbar actions use
// Markdown, including the local ++underline++ extension handled below.
function safeInlineFormatting() {
  return (tree: HastNode) => transform(tree);
}

function transform(parent: HastNode) {
  if (!parent.children) return;
  const output: HastNode[] = [];
  const stack: Array<{ tag: string; node: HastNode; target: HastNode[] }> = [];
  let target = output;

  for (const child of parent.children) {
    if (child.type === "raw") {
      const raw = child.value?.trim() ?? "";
      const colorOpen = raw.match(/^<span\s+style=["']color:\s*(#[0-9a-f]{6})\s*;?["']>$/i);
      const close = raw.match(/^<\/span>$/i);
      if (colorOpen) {
        const tag = "span";
        const node: HastNode = { type: "element", tagName: tag, properties: { style: `color: ${colorOpen[1]}` }, children: [] };
        target.push(node);
        stack.push({ tag, node, target });
        target = node.children!;
        continue;
      }
      if (close && stack[stack.length - 1]?.tag === close[1].toLowerCase()) {
        target = stack.pop()!.target;
        continue;
      }
      target.push({ type: "text", value: raw });
      continue;
    }
    if (child.type === "text" && parent.tagName !== "code" && parent.tagName !== "pre" && (child.value?.includes("++") || child.value?.includes("[["))) {
      target.push(...inlineFormattingNodes(child.value));
      continue;
    }
    transform(child);
    target.push(child);
  }
  parent.children = output;
}

function inlineFormattingNodes(value: string): HastNode[] {
  const nodes: HastNode[] = [];
  const pattern = /\+\+([^+\n]+)\+\+|\[\[([^\]\n]+)\]\]/g;
  let cursor = 0;
  for (const match of value.matchAll(pattern)) {
    const index = match.index ?? 0;
    if (index > cursor) nodes.push({ type: "text", value: value.slice(cursor, index) });
    if (match[1]) {
      nodes.push({ type: "element", tagName: "u", properties: {}, children: [{ type: "text", value: match[1] }] });
    } else {
      const [rawTarget, rawLabel] = match[2].split("|", 2);
      const target = rawTarget.trim();
      const parts = target.split("/");
      const label = rawLabel?.trim() || parts[parts.length - 1]?.replace(/\.md$/, "") || target;
      nodes.push({ type: "element", tagName: "a", properties: { href: `#wiki=${encodeURIComponent(target)}`, className: ["wiki-link"] }, children: [{ type: "text", value: label }] });
    }
    cursor = index + match[0].length;
  }
  if (cursor < value.length) nodes.push({ type: "text", value: value.slice(cursor) });
  return nodes.length ? nodes : [{ type: "text", value }];
}

function withoutFrontmatter(content: string) {
  if (!content.startsWith("---\n")) return content;
  const closing = content.indexOf("\n---", 4);
  return closing < 0 ? content : content.slice(closing + 4).replace(/^\s+/, "");
}

export function MarkdownPreview({ content, onOpenWikiLink }: { content: string; onOpenWikiLink?: (target: string) => void }) {
  return <article className="prose">
    <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[safeInlineFormatting]} components={{ a: ({ href, children, ...props }) => {
      if (href?.startsWith("#wiki=")) {
        const target = decodeURIComponent(href.slice(6));
        return <a {...props} href={href} onClick={(event) => { event.preventDefault(); onOpenWikiLink?.(target); }}>{children}</a>;
      }
      return <a {...props} href={href}>{children}</a>;
    } }}>{withoutFrontmatter(content)}</ReactMarkdown>
  </article>;
}
